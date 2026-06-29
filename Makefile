.PHONY: build clean test package deploy help

GOATFLOW_DIR := $(shell realpath ../goatflow 2>/dev/null || echo /home/nigel/git/goatkit/goatflow)
-include $(GOATFLOW_DIR)/.env
export

BINARY := kb
PLUGIN_BINARY := kb
BUILD_DIR := bin
CMD_DIR := ./cmd/kb-plugin
VERSION := $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --abbrev-ref HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)
GOATFLOW_URL ?= http://localhost:8080
GO_IMAGE ?= golang:1.25.10-alpine

build:
	@echo "Building $(PLUGIN_BINARY) $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	docker run --rm \
		-u "$$(id -u):$$(id -g)" \
		-v "$(CURDIR)":/src \
		-v "$(GOATFLOW_DIR)":/goatflow \
		-w /src \
		-e GOCACHE=/tmp/gocache \
		-e GOMODCACHE=/tmp/gomod \
		$(GO_IMAGE) sh -c "go mod download && go build -buildvcs=false -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/$(PLUGIN_BINARY) $(CMD_DIR)"

clean:
	@rm -rf $(BUILD_DIR)

test:
	docker run --rm \
		-u "$$(id -u):$$(id -g)" \
		-v "$(CURDIR)":/src \
		-v "$(GOATFLOW_DIR)":/goatflow \
		-w /src \
		-e GOCACHE=/tmp/gocache \
		-e GOMODCACHE=/tmp/gomod \
		$(GO_IMAGE) sh -c "go test -buildvcs=false ./..."

package: build
	@echo "Packaging $(BINARY) $(VERSION)..."
	@rm -f $(BUILD_DIR)/$(BINARY).zip
	@cd $(BUILD_DIR) && cp ../plugin.yaml . && zip -q -r $(BINARY).zip $(PLUGIN_BINARY) plugin.yaml && rm -f plugin.yaml
	@echo "Package: $(BUILD_DIR)/$(BINARY).zip"

deploy: package
	@test -n "$(ADMIN_API_KEY)$(ADMIN_PASSWORD)" || { echo "ERROR: ADMIN_API_KEY or ADMIN_PASSWORD missing from $(GOATFLOW_DIR)/.env"; exit 1; }
	@echo "Deploying $(BINARY) to $(GOATFLOW_URL)..."
	@TOKEN="$(ADMIN_API_KEY)"; \
	RESP=""; \
	if [ -n "$$TOKEN" ]; then \
		RESP=$$(curl -sk "$(GOATFLOW_URL)/api/v1/plugins/upload" -H "Authorization: Bearer $$TOKEN" -H "Accept: application/json" -F "plugin=@$(BUILD_DIR)/$(BINARY).zip"); \
	fi; \
	case "$$RESP" in \
		*\"Invalid\ or\ expired\ token\"*|*\"success\":false*|\"\") \
			PAYLOAD=$$(python3 -c 'import json, os; print(json.dumps({"login": os.environ.get("ADMIN_USER", ""), "password": os.environ.get("ADMIN_PASSWORD", "")}))'); \
			TOKEN=$$(curl -sk "$(GOATFLOW_URL)/api/auth/login" -H "Content-Type: application/json" -H "Accept: application/json" -d "$$PAYLOAD" | python3 -c 'import json, sys; data=json.load(sys.stdin); print(data.get("access_token") or data.get("token") or "")'); \
			if [ -z "$$TOKEN" ]; then echo "ERROR: Auth failed"; exit 1; fi; \
			RESP=$$(curl -sk "$(GOATFLOW_URL)/api/v1/plugins/upload" -H "Authorization: Bearer $$TOKEN" -H "Accept: application/json" -F "plugin=@$(BUILD_DIR)/$(BINARY).zip"); \
			;; \
	esac; \
	echo "$$RESP" | python3 -m json.tool 2>/dev/null || echo "$$RESP"

help:
	@echo "GoatFlow KB plugin"
	@echo ""
	@echo "Usage:"
	@echo "  make build                   Build the gRPC plugin binary"
	@echo "  make test                    Run plugin tests"
	@echo "  make package                 Build and ZIP plugin.yaml + binary"
	@echo "  make deploy                  Package and upload via GoatFlow API"
	@echo "  make deploy GOATFLOW_URL=..  Deploy to a specific GoatFlow instance"
