# EMS edge (cmd/edge) build & packaging for the Siemens IOT2050.
# The cloud stack keeps its docker-compose flow (deploy/); these
# targets only cover the edge binary, which is deployed manually
# (pinned versions, no watchtower — see docs/runbooks/edge_deploy.md).

EDGE_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
EDGE_LDFLAGS := -s -w -X main.version=$(EDGE_VERSION)
DIST         := dist

.PHONY: edge edge-arm64 edge-package edge-test clean

# Host-arch build for local development / replay runs.
edge:
	go build -ldflags '$(EDGE_LDFLAGS)' -o $(DIST)/ems-edge ./cmd/edge

# Static linux/arm64 binary for the IOT2050 (pure Go: CGO off, the
# SQLite driver is modernc.org/sqlite, so no cross-toolchain needed).
edge-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
		-ldflags '$(EDGE_LDFLAGS)' -o $(DIST)/ems-edge-linux-arm64 ./cmd/edge

# Deployable tarball: binary + systemd unit + env/config templates +
# the register catalog the config references.
edge-package: edge-arm64
	rm -rf $(DIST)/edge-pkg
	mkdir -p $(DIST)/edge-pkg
	cp $(DIST)/ems-edge-linux-arm64 $(DIST)/edge-pkg/ems-edge
	cp deploy/edge/ems-edge.service $(DIST)/edge-pkg/
	cp deploy/edge/edge.env.example $(DIST)/edge-pkg/
	cp config.edge.example.yaml $(DIST)/edge-pkg/
	cp registers/huawei_smartlogger.yaml $(DIST)/edge-pkg/
	tar -C $(DIST)/edge-pkg -czf $(DIST)/ems-edge_$(EDGE_VERSION)_linux_arm64.tar.gz .
	@echo "package: $(DIST)/ems-edge_$(EDGE_VERSION)_linux_arm64.tar.gz"

edge-test:
	go test ./internal/edge/... ./cmd/edge/...

clean:
	rm -rf $(DIST)
