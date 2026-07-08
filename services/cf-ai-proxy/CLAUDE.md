# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Commands

- `go build`: Build the project
- `go test`: Run all tests
- `go test -run TestName`: Run a specific test
- `go fmt`: Format code
- `go vet`: Check for code issues
- `go mod tidy`: Update dependencies

## Architecture

The codebase is structured as follows:

1. **cmd**: Contains the main binary entry points
2. **models**: Data models and schemas (defined in models.csv)
3. **proxy**: Core AI proxy logic
4. **utils**: Helper functions and utilities

The code leverages Go's module system with dependencies listed in go.mod. Tests are located in directories ending with `_test`.

## Workflows

1. **Model Updates**: 
   - Modify `models.csv` for new model definitions
   - Run `go generate` to regenerate code from the updated schema

2. **Proxy Configuration**:
   - Configure via environment variables
   - Main configuration file: `config.json`

3. **Testing**:
   - Unit tests in `*_test.go` files
   - Integration tests in the `tests` directory

Let me know if you'd like me to add or modify any sections!
