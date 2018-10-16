# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: giga android ios giga-cross swarm evm all test clean
.PHONY: giga-linux giga-linux-386 giga-linux-amd64 giga-linux-mips64 giga-linux-mips64le
.PHONY: giga-linux-arm giga-linux-arm-5 giga-linux-arm-6 giga-linux-arm-7 giga-linux-arm64
.PHONY: giga-darwin giga-darwin-386 giga-darwin-amd64
.PHONY: giga-windows giga-windows-386 giga-windows-amd64

GOBIN = $(shell pwd)/build/bin
GO ?= latest

giga:
	build/env.sh go run build/ci.go install ./cmd/giga
	@echo "Done building."
	@echo "Run \"$(GOBIN)/giga\" to launch giga."

swarm:
	build/env.sh go run build/ci.go install ./cmd/swarm
	@echo "Done building."
	@echo "Run \"$(GOBIN)/swarm\" to launch swarm."

all:
	build/env.sh go run build/ci.go install

android:
	build/env.sh go run build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/giga.aar\" to use the library."

ios:
	build/env.sh go run build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/giga.framework\" to use the library."

test: all
	build/env.sh go run build/ci.go test

lint: ## Run linters.
	build/env.sh go run build/ci.go lint

clean:
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go get -u golang.org/x/tools/cmd/stringer
	env GOBIN= go get -u github.com/kevinburke/go-bindata/go-bindata
	env GOBIN= go get -u github.com/fjl/gencodec
	env GOBIN= go get -u github.com/golang/protobuf/protoc-gen-go
	env GOBIN= go install ./cmd/abigen
	@type "npm" 2> /dev/null || echo 'Please install node.js and npm'
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Cross Compilation Targets (xgo)

giga-cross: giga-linux giga-darwin giga-windows giga-android giga-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/giga-*

giga-linux: giga-linux-386 giga-linux-amd64 giga-linux-arm giga-linux-mips64 giga-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-*

giga-linux-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/geth
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep 386

giga-linux-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/geth
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep amd64

giga-linux-arm: giga-linux-arm-5 giga-linux-arm-6 giga-linux-arm-7 giga-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep arm

giga-linux-arm-5:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/geth
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep arm-5

giga-linux-arm-6:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/geth
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep arm-6

giga-linux-arm-7:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/geth
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep arm-7

giga-linux-arm64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/geth
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep arm64

giga-linux-mips:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep mips

giga-linux-mipsle:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep mipsle

giga-linux-mips64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep mips64

giga-linux-mips64le:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/giga-linux-* | grep mips64le

giga-darwin: giga-darwin-386 giga-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/giga-darwin-*

giga-darwin-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/geth
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/giga-darwin-* | grep 386

giga-darwin-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/geth
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/giga-darwin-* | grep amd64

giga-windows: giga-windows-386 giga-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/giga-windows-*

giga-windows-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/geth
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/giga-windows-* | grep 386

giga-windows-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/geth
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/giga-windows-* | grep amd64
