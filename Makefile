# NatPunch Makefile —— 本地开发 / 发布前全链路自查（阶段四 F4-4）
GO      ?= go
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "(dev)")
LDFLAGS := -s -w -X github.com/NekoBoxHQ/NatPunch/lib/version.VERSION=$(VERSION)

.PHONY: all build build-server build-client vet test test-race integration lint vuln cross smoke clean

all: vet test build

build: build-server build-client

build-server:
	$(GO) build -ldflags "$(LDFLAGS)" -o natpunch ./cmd/natpunch/natpunch.go

build-client:
	$(GO) build -ldflags "$(LDFLAGS)" -o natpunch-client ./cmd/npc/npc.go

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

integration:
	$(GO) test -tags integration ./lib/nps_mux/ -timeout 20m

lint:
	golangci-lint run

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

# 发布矩阵交叉编译（与 release.yml 一致：linux amd64/arm64/armv7/mipsle × server/client）
CROSS_ARCHS := amd64 arm64 arm mipsle
CROSS_GOARM := 7

cross: cross-server cross-client

cross-server:
	@for a in $(CROSS_ARCHS); do \
		echo "==> server linux/$$a"; \
		GOOS=linux GOARCH=$$a CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o /tmp/natpunch-$$a ./cmd/natpunch/natpunch.go || exit 1; \
	done

cross-client:
	@for a in $(CROSS_ARCHS); do \
		echo "==> client linux/$$a"; \
		GOOS=linux GOARCH=$$a CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o /tmp/natpunch-client-$$a ./cmd/npc/npc.go || exit 1; \
	done

# 冒烟：独立临时目录启动服务端，验证端口监听与仓库 conf 零污染
# 用法：make smoke DIR=$(mktemp -d)
smoke:
	@if [ -z "$(DIR)" ]; then echo "usage: make smoke DIR=<tempdir>"; exit 1; fi
	mkdir -p "$(DIR)/conf"
	cp conf/natpunch.conf "$(DIR)/conf/"
	$(GO) build -ldflags "$(LDFLAGS)" -o "$(DIR)/natpunch" ./cmd/natpunch/natpunch.go
	cd "$(DIR)" && ./natpunch -conf_path="$(DIR)"
	@echo "smoke: 检查 $${DIR}/natpunch.log 中端口监听日志；确认仓库 conf/ 未被修改"

clean:
	rm -f natpunch natpunch-client
