package main

const dashboardHTML = `
<!DOCTYPE html>
<html lang="vi">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>CF Pool Monitor - Bảng Điều Khiển</title>
    <link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
    <style>
        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
            font-family: 'Plus Jakarta Sans', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        }
        body {
            background-color: #0b0c10;
            color: #c5c6c7;
            padding: 2rem;
            min-height: 100vh;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
        }
        /* Header styling */
        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 2.5rem;
            padding-bottom: 1.5rem;
            border-bottom: 1px solid rgba(255, 255, 255, 0.05);
        }
        .logo-section h1 {
            font-size: 2rem;
            font-weight: 700;
            background: linear-gradient(135deg, #00f2fe 0%, #4facfe 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            margin-bottom: 0.25rem;
        }
        .logo-section p {
            font-size: 0.875rem;
            color: #666f7d;
        }
        .controls-section {
            display: flex;
            gap: 1rem;
            align-items: center;
        }
        .btn-refresh {
            background: rgba(79, 172, 254, 0.1);
            color: #4facfe;
            border: 1px solid rgba(79, 172, 254, 0.2);
            padding: 0.625rem 1.25rem;
            border-radius: 8px;
            font-weight: 600;
            cursor: pointer;
            display: flex;
            align-items: center;
            gap: 0.5rem;
            transition: all 0.2s ease;
        }
        .btn-refresh:hover {
            background: rgba(79, 172, 254, 0.2);
            border-color: #4facfe;
        }
        /* Overview grid */
        .overview-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 1.5rem;
            margin-bottom: 2.5rem;
        }
        .metric-card {
            background: rgba(25, 28, 47, 0.4);
            backdrop-filter: blur(12px);
            border: 1px solid rgba(255, 255, 255, 0.05);
            padding: 1.5rem;
            border-radius: 16px;
            box-shadow: 0 8px 32px 0 rgba(0, 0, 0, 0.2);
        }
        .metric-card h3 {
            font-size: 0.875rem;
            font-weight: 500;
            color: #666f7d;
            margin-bottom: 0.5rem;
            text-transform: uppercase;
            letter-spacing: 0.05em;
        }
        .metric-card .value {
            font-size: 2.25rem;
            font-weight: 700;
            color: #ffffff;
        }
        /* Filter and accounts grid */
        .filter-section {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 1.5rem;
        }
        .filter-buttons {
            display: flex;
            gap: 0.5rem;
        }
        .filter-btn {
            background: rgba(255, 255, 255, 0.03);
            border: 1px solid rgba(255, 255, 255, 0.05);
            color: #8c9ba5;
            padding: 0.5rem 1rem;
            border-radius: 6px;
            cursor: pointer;
            font-size: 0.875rem;
            font-weight: 500;
            transition: all 0.2s;
        }
        .filter-btn.active, .filter-btn:hover {
            background: rgba(79, 172, 254, 0.15);
            border-color: rgba(79, 172, 254, 0.4);
            color: #ffffff;
        }
        .accounts-grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
            gap: 1.5rem;
        }
        .account-card {
            background: rgba(25, 28, 47, 0.4);
            backdrop-filter: blur(12px);
            border: 1px solid rgba(255, 255, 255, 0.05);
            border-radius: 16px;
            padding: 1.25rem;
            transition: all 0.2s ease;
        }
        .account-card:hover {
            transform: translateY(-2px);
            border-color: rgba(79, 172, 254, 0.2);
            box-shadow: 0 10px 40px 0 rgba(0, 0, 0, 0.3);
        }
        .card-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 1rem;
        }
        .account-name {
            font-weight: 600;
            color: #ffffff;
            font-size: 1rem;
        }
        .status-badge {
            font-size: 0.75rem;
            font-weight: 600;
            padding: 0.25rem 0.625rem;
            border-radius: 20px;
            display: flex;
            align-items: center;
            gap: 0.375rem;
        }
        .status-badge.active {
            background: rgba(16, 185, 129, 0.1);
            color: #10b981;
        }
        .status-badge.penalized {
            background: rgba(245, 158, 11, 0.1);
            color: #f59e0b;
        }
        .status-badge.locked {
            background: rgba(239, 68, 68, 0.1);
            color: #ef4444;
        }
        .dot {
            width: 6px;
            height: 6px;
            border-radius: 50%;
            display: inline-block;
        }
        .dot.active {
            background-color: #10b981;
            box-shadow: 0 0 8px #10b981;
            animation: pulse 1.5s infinite;
        }
        .dot.penalized {
            background-color: #f59e0b;
        }
        .dot.locked {
            background-color: #ef4444;
        }
        .card-body {
            margin-bottom: 1rem;
        }
        .usage-text {
            display: flex;
            justify-content: space-between;
            font-size: 0.875rem;
            margin-bottom: 0.5rem;
            color: #8c9ba5;
        }
        .usage-value {
            font-weight: 600;
            color: #ffffff;
        }
        .progress-bar-container {
            width: 100%;
            height: 8px;
            background: rgba(255, 255, 255, 0.05);
            border-radius: 4px;
            overflow: hidden;
        }
        .progress-bar {
            height: 100%;
            width: 0%;
            border-radius: 4px;
            transition: width 0.5s ease-out;
        }
        .progress-bar.green {
            background: linear-gradient(90deg, #10b981, #34d399);
        }
        .progress-bar.orange {
            background: linear-gradient(90deg, #f59e0b, #fbbf24);
        }
        .progress-bar.red {
            background: linear-gradient(90deg, #ef4444, #f87171);
        }
        .card-footer {
            font-size: 0.75rem;
            color: #666f7d;
            border-top: 1px solid rgba(255, 255, 255, 0.05);
            padding-top: 0.75rem;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .copy-btn {
            background: none;
            border: none;
            color: #4facfe;
            cursor: pointer;
            font-weight: 500;
        }
        .copy-btn:hover {
            text-decoration: underline;
        }
        @keyframes pulse {
            0% { transform: scale(0.9); opacity: 0.6; }
            50% { transform: scale(1.2); opacity: 1; }
            100% { transform: scale(0.9); opacity: 0.6; }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo-section">
                <h1>CF Pool Monitor</h1>
                <p>Hệ thống giám sát xoay vòng tài khoản Cloudflare Workers AI</p>
            </div>
            <div class="controls-section">
                <button class="btn-refresh" id="refreshBtn" onclick="fetchMetrics()">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg>
                    Làm mới
                </button>
            </div>
        </header>

        <div class="overview-grid">
            <div class="metric-card">
                <h3>Tổng Tài Khoản</h3>
                <div class="value" id="totalAccounts">0</div>
            </div>
            <div class="metric-card">
                <h3>Tài Khoản Hoạt Động</h3>
                <div class="value" id="activeAccounts">0</div>
            </div>
            <div class="metric-card">
                <h3>Neurons Đã Dùng Hôm Nay</h3>
                <div class="value" id="totalNeurons">0</div>
            </div>
        </div>

        <div class="filter-section">
            <div class="filter-buttons">
                <button class="filter-btn active" onclick="setFilter('all')">Tất cả</button>
                <button class="filter-btn" onclick="setFilter('active')">Đang chạy</button>
                <button class="filter-btn" onclick="setFilter('penalized')">Bị phạt/Khóa</button>
            </div>
        </div>

        <div class="accounts-grid" id="accountsGrid">
            <!-- Nạp động qua JS -->
        </div>
    </div>

    <script>
        let currentFilter = 'all';
        let rawMetrics = null;

        function setFilter(filter) {
            currentFilter = filter;
            document.querySelectorAll('.filter-btn').forEach(btn => {
                btn.classList.remove('active');
            });
            event.target.classList.add('active');
            renderAccounts();
        }

        async function fetchMetrics() {
            const btn = document.getElementById('refreshBtn');
            btn.style.opacity = '0.5';
            btn.disabled = true;

            try {
                const response = await fetch('/admin/metrics');
                const data = await response.json();
                rawMetrics = data;

                document.getElementById('totalAccounts').textContent = data.total_accounts;
                document.getElementById('activeAccounts').textContent = data.active_accounts;
                document.getElementById('totalNeurons').textContent = data.total_neurons_used.toLocaleString();

                renderAccounts();
            } catch (err) {
                console.error("Lỗi khi tải metrics:", err);
            } finally {
                btn.style.opacity = '1';
                btn.disabled = false;
            }
        }

        function renderAccounts() {
            const grid = document.getElementById('accountsGrid');
            grid.innerHTML = '';

            if (!rawMetrics || !rawMetrics.accounts) return;

            rawMetrics.accounts.forEach(acc => {
                let status = 'active';
                let statusText = 'Đang chạy';
                
                if (acc.is_penalized) {
                    status = 'penalized';
                    const min = Math.ceil(acc.seconds_remaining / 60);
                    const hours = Math.floor(min / 60);
                    const remMin = min % 60;
                    statusText = hours > 0 ? "Phạt: " + hours + "h " + remMin + "m" : "Phạt: " + min + "m";
                } else if (!acc.is_active) {
                    status = 'locked';
                    statusText = 'Đạt giới hạn';
                }

                if (currentFilter === 'active' && status !== 'active') return;
                if (currentFilter === 'penalized' && status === 'active') return;

                const percent = Math.min((acc.current_neurons_used / 9500) * 100, 100);
                
                let progressColor = 'green';
                if (percent >= 90) progressColor = 'red';
                else if (percent >= 70) progressColor = 'orange';

                const card = document.createElement('div');
                card.className = 'account-card';
                card.innerHTML = 
                    '<div class="card-header">' +
                    '    <span class="account-name">' + acc.account_id.slice(0, 8) + '...' + acc.account_id.slice(-4) + '</span>' +
                    '    <span class="status-badge ' + status + '">' +
                    '        <span class="dot ' + status + '"></span> ' + statusText +
                    '    </span>' +
                    '</div>' +
                    '<div class="card-body">' +
                    '    <div class="usage-text">' +
                    '        <span>Sử dụng</span>' +
                    '        <span class="usage-value">' + acc.current_neurons_used.toLocaleString() + ' / 9,500</span>' +
                    '    </div>' +
                    '    <div class="progress-bar-container">' +
                    '        <div class="progress-bar ' + progressColor + '" style="width: ' + percent + '%"></div>' +
                    '    </div>' +
                    '</div>' +
                    '<div class="card-footer">' +
                    '    <span>Hạn mức: 10,000 Neurons</span>' +
                    '    <button class="copy-btn" onclick="navigator.clipboard.writeText(\'' + acc.account_id + '\')">Copy ID</button>' +
                    '</div>';
                grid.appendChild(card);
            });
        }

        // Tự động tải lúc đầu và tự động làm mới mỗi 5 giây
        fetchMetrics();
        setInterval(fetchMetrics, 5000);
    </script>
</body>
</html>
`
