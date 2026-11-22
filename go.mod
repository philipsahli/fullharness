module github.com/philipsahli/fullharness

go 1.27.2

toolchain go1.27.4

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/coder/websocket v1.8.15
)

require github.com/coder/acp-go-sdk v0.20.0 // indirect

// web/node_modules holds a Go package (npm "flatted"); keep it out of ./...
ignore ./web/node_modules
