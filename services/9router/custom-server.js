const http = require("http");
const path = require("path");
const fs = require("fs");
const crypto = require("crypto");

const origCreate = http.createServer.bind(http);

// Per-process secret proving x-9r-real-ip was stamped below rather than sent by the client.
const PEER_TOKEN = crypto.randomBytes(24).toString("hex");
process.env.NINEROUTER_PEER_TOKEN = PEER_TOKEN;

// ─── 1. Runtime In-Memory / File Patches ───
function applyRuntimePatches() {
  const chunksDir = path.join(__dirname, ".next", "server", "chunks");
  if (!fs.existsSync(chunksDir)) return;

  // Patch 1: Allow 403 status to trigger OAuth token refresh during connection test (827.js)
  const file827 = path.join(chunksDir, "827.js");
  if (fs.existsSync(file827)) {
    try {
      let code = fs.readFileSync(file827, "utf8");
      const target = "401===g.status&&c.refreshable";
      const replacement = "(401===g.status||403===g.status)&&c.refreshable";
      if (code.includes(target)) {
        code = code.replace(target, replacement);
        fs.writeFileSync(file827, code, "utf8");
        console.log("✅ [9Router Patch] Successfully patched 827.js (Auto-refresh on 401 & 403 in Test button)");
      }
    } catch (e) {
      console.warn("⚠️ [9Router Patch] Failed to patch 827.js:", e.message);
    }
  }

  // Patch 2: Sanitize competitive branding & billing header in system prompts for Antigravity (436.js)
  const file436 = path.join(chunksDir, "436.js");
  if (fs.existsSync(file436)) {
    try {
      let code = fs.readFileSync(file436, "utf8");
      const target = `y=[{from:"You are a Claude agent, built on Anthropic's Claude Agent SDK.",to:""},{from:/opencode/gi,to:a=>"OpenCode"===a?"Antigravity":"OPENCODE"===a?"ANTIGRAVITY":"antigravity"}]`;
      const replacement = `y=[{from:/^x-anthropic-billing-header:[^\\n]*(?:\\r?\\n)*/gim,to:""},{from:/You are Claude Code[^\\n]*/gi,to:"You are Antigravity, an intelligent agentic AI coding assistant by Google DeepMind."},{from:/Anthropic's official CLI[^\\n]*/gi,to:""},{from:"You are a Claude agent, built on Anthropic's Claude Agent SDK.",to:"You are Antigravity, built by Google DeepMind."},{from:/You are Hermes Agent[^\\n]*/gi,to:"You are Antigravity."},{from:/opencode/gi,to:a=>"OpenCode"===a?"Antigravity":"OPENCODE"===a?"ANTIGRAVITY":"antigravity"}]`;
      if (code.includes(target)) {
        code = code.replace(target, replacement);
        fs.writeFileSync(file436, code, "utf8");
        console.log("✅ [9Router Patch] Successfully patched 436.js (Antigravity Prompt Cloaking & Anti-429)");
      }
    } catch (e) {
      console.warn("⚠️ [9Router Patch] Failed to patch 436.js:", e.message);
    }
  }
}

// ─── 2. Robust Standalone Background Token Refresh ───
let _googleClients = null;
async function getGoogleClient(provider) {
  if (!_googleClients) {
    try {
      const shared = await import(path.join(__dirname, "open-sse", "providers", "shared.js"));
      _googleClients = {
        antigravity: shared.ANTIGRAVITY_OAUTH_CLIENT,
        "gemini-cli": shared.GOOGLE_OAUTH_CLIENT,
      };
    } catch (e) {
      console.warn("⚠️ [BG_TOKEN_REFRESH] Could not dynamically load OAuth clients:", e.message);
      _googleClients = {};
    }
  }
  return _googleClients[provider] || null;
}

let bgRefreshRunning = false;

async function refreshSingleGoogleAccount(conn, data, clientConfig, db) {
  const maxRetries = 3;
  let lastError = null;

  for (let attempt = 1; attempt <= maxRetries; attempt++) {
    try {
      const res = await fetch("https://oauth2.googleapis.com/token", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          grant_type: "refresh_token",
          refresh_token: data.refreshToken,
          client_id: clientConfig.clientId,
          client_secret: clientConfig.clientSecret,
        }),
      });

      if (!res.ok) {
        const errText = await res.text();
        console.warn(`[BG_TOKEN_REFRESH] HTTP ${res.status} refreshing ${conn.email || conn.name}: ${errText}`);
        return false;
      }

      const tokens = await res.json();
      data.accessToken = tokens.access_token;
      data.expiresIn = tokens.expires_in || 3599;
      data.expiresAt = new Date(Date.now() + (tokens.expires_in || 3599) * 1000).toISOString();
      data.lastRefreshAt = new Date().toISOString();
      if (tokens.refresh_token) data.refreshToken = tokens.refresh_token;

      // Clean locks and error states
      data.testStatus = "active";
      data.lastError = null;
      data.errorCode = null;
      data.backoffLevel = 0;
      for (const k of Object.keys(data)) {
        if (k.startsWith("modelLock_")) data[k] = null;
      }

      const nowIso = new Date().toISOString();
      db.prepare("UPDATE providerConnections SET data = ?, updatedAt = ? WHERE id = ?").run(
        JSON.stringify(data),
        nowIso,
        conn.id
      );

      console.log(`ℹ️  [BG_TOKEN_REFRESH] Successfully refreshed ${conn.provider} account: ${conn.email || conn.name} (valid until ${data.expiresAt})`);
      return true;
    } catch (err) {
      lastError = err;
      if (attempt < maxRetries) {
        await new Promise((r) => setTimeout(r, 2000));
      }
    }
  }

  console.warn(`❌ [BG_TOKEN_REFRESH] Failed to refresh ${conn.email || conn.name} after ${maxRetries} attempts: ${lastError?.message}`);
  return false;
}

async function runBackgroundTokenRefreshTick() {
  if (bgRefreshRunning) return;
  bgRefreshRunning = true;

  try {
    const dbPath = path.join(process.env.DATA_DIR || "/app/data", "db", "data.sqlite");
    if (!fs.existsSync(dbPath)) return;

    const Database = require("better-sqlite3");
    const db = new Database(dbPath);

    const rows = db.prepare("SELECT * FROM providerConnections WHERE isActive = 1 AND authType = 'oauth'").all();
    const now = Date.now();
    // Proactively refresh if token expires within 20 minutes (1200 seconds)
    const REFRESH_THRESHOLD_MS = 20 * 60 * 1000;

    for (const row of rows) {
      try {
        const data = JSON.parse(row.data);
        if (!data || !data.refreshToken) continue;

        const clientConfig = await getGoogleClient(row.provider);
        if (!clientConfig || !clientConfig.clientId) continue;

        const expMs = data.expiresAt ? new Date(data.expiresAt).getTime() : 0;
        const diffMs = expMs - now;

        if (diffMs < REFRESH_THRESHOLD_MS) {
          await refreshSingleGoogleAccount(row, data, clientConfig, db);
          await new Promise((r) => setTimeout(r, 2000));
        }
      } catch (err) {
        console.warn(`[BG_TOKEN_REFRESH] Error processing connection ${row.id}:`, err.message);
      }
    }
  } catch (e) {
    console.warn("[BG_TOKEN_REFRESH] Tick error:", e.message);
  } finally {
    bgRefreshRunning = false;
  }
}

let backgroundRefreshStarted = false;
function startEnhancedBackgroundTokenRefresh() {
  if (backgroundRefreshStarted) return;
  backgroundRefreshStarted = true;

  console.log("🚀 [BG_TOKEN_REFRESH] Enhanced Background OAuth Refresh Service started (5m interval)");
  setTimeout(() => {
    runBackgroundTokenRefreshTick().catch(() => {});
  }, 5000);

  const intervalHandle = setInterval(() => {
    runBackgroundTokenRefreshTick().catch(() => {});
  }, 5 * 60 * 1000);
  if (intervalHandle.unref) intervalHandle.unref();
}

// ─── 3. HTTP Server Wrapper ───
http.createServer = (...args) => {
  const handler = args.find((a) => typeof a === "function");
  const rest = args.filter((a) => typeof a !== "function");
  if (!handler) return origCreate(...args);
  const wrapped = (req, res) => {
    const socketIp = req.socket && req.socket.remoteAddress ? req.socket.remoteAddress : "";
    const xff = req.headers["x-forwarded-for"];
    const xRealIp = req.headers["x-real-ip"];
    const viaProxy = !!(xff || xRealIp);
    const isLoopbackProxy = socketIp === "127.0.0.1" || socketIp === "::1" || socketIp === "::ffff:127.0.0.1";
    const proxyIp = xRealIp || (xff ? String(xff).split(",")[0].trim() : "");
    const ip = isLoopbackProxy && proxyIp ? proxyIp : socketIp;
    delete req.headers["x-9r-real-ip"];
    delete req.headers["x-forwarded-for"];
    delete req.headers["x-9r-via-proxy"];
    delete req.headers["x-9r-peer-token"];
    req.headers["x-9r-real-ip"] = ip;
    req.headers["x-9r-peer-token"] = PEER_TOKEN;
    if (viaProxy) req.headers["x-9r-via-proxy"] = "1";
    return handler(req, res);
  };
  const server = origCreate(...rest, wrapped);
  server.once("listening", () => {
    startEnhancedBackgroundTokenRefresh();
  });
  const origEmit = server.emit;
  server.emit = function (event, ...eventArgs) {
    const [req, socket, head] = eventArgs;
    if (event !== "upgrade" || String(req.headers.upgrade || "").toLowerCase() !== "h2c") {
      return origEmit.call(this, event, ...eventArgs);
    }

    const contentLength = Number(req.headers["content-length"] || 0);
    if (!Number.isSafeInteger(contentLength) || contentLength < 0) {
      socket.destroy();
      return true;
    }
    const chunks = [head];
    let received = head.length;
    const serve = () => {
      const replay = new http.IncomingMessage(socket);
      Object.assign(replay, { method: req.method, url: req.url, headers: req.headers, complete: true });
      if (received) replay.push(Buffer.concat(chunks, received).subarray(0, contentLength));
      replay.push(null);
      const res = new http.ServerResponse(replay);
      res.shouldKeepAlive = false;
      res.assignSocket(socket);
      res.once("finish", () => socket.end());
      Promise.resolve().then(() => wrapped(replay, res)).catch((error) => {
        console.error("Failed to downgrade h2c request", error);
        socket.destroy();
      });
    };
    if (received >= contentLength) serve();
    else {
      socket.on("data", function readBody(chunk) {
        chunks.push(chunk);
        received += chunk.length;
        if (received < contentLength) return;
        socket.off("data", readBody);
        serve();
      });
      socket.resume();
    }
    delete req.headers.upgrade;
    delete req.headers["http2-settings"];
    req.headers.connection = "close";
    return true;
  };
  return server;
};

// ─── 4. Boot Entry ───
if (require.main === module) {
  applyRuntimePatches();

  const standalone = path.join(__dirname, "server.js");
  if (fs.existsSync(standalone)) {
    require(standalone);
  } else {
    const nextBin = require.resolve("next/dist/bin/next");
    process.argv = [process.argv[0], nextBin, "start", ...process.argv.slice(2)];
    require(nextBin);
  }
}
