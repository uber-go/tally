BENCH_FLAGS ?= -cpuprofile=cpu.pprof -memprofile=mem.pprof -benchmem
PKGS ?= $(shell go list ./...)
PKG_FILES ?= $(shell go list -f '{{ .GoFiles }}' ./...)
LINT_IGNORE = m3/thrift\|thirdparty
LICENSE_IGNORE = thirdparty
GO = GO111MODULE=on go

.PHONY: all
all: lint test

.PHONY: dependencies
dependencies:
	@echo "Installing test dependencies..."
	$(GO) get github.com/axw/gocov/gocov
	$(GO) get github.com/mattn/goveralls
	@echo "Installing golint..."
	$(GO) get -u golang.org/x/lint/golint

.PHONY: lint
lint:
	@rm -rf lint.log
	@echo "Checking formatting..."
	@gofmt -d -s $(PKG_FILES) 2>&1 | grep -v '$(LINT_IGNORE)' | tee lint.log
	@echo "Installing test dependencies for vet..."
	@go test -i $(PKGS)
	@echo "Checking lint..."
	@$(foreach dir,$(PKGS),golint $(dir) 2>&1 | grep -v '$(LINT_IGNORE)' | tee -a lint.log;)
	@echo "Checking for unresolved FIXMEs..."
	@git grep -i fixme | grep -v -e vendor -e Makefile | grep -v '$(LINT_IGNORE)' | tee -a lint.log
	@echo "Checking for license headers..."
	@./check_license.sh | grep -v '$(LICENSE_IGNORE)' | tee -a lint.log
	@[ ! -s lint.log ]

.PHONY: test
test:
	$(GO) test -race -v $(PKGS)

.PHONY: examples
examples:
	mkdir -p ./bin
	$(GO) build -o ./bin/print_example ./example/
	$(GO) build -o ./bin/m3_example ./m3/example/
	$(GO) build -o ./bin/prometheus_example ./prometheus/example/
	$(GO) build -o ./bin/statsd_example ./statsd/example/

.PHONY: cover
cover:
	$(GO) test -cover -coverprofile cover.out -race -v $(PKGS)

.PHONY: coveralls
coveralls:
	goveralls -service=travis-ci || echo "Coveralls failed"

.PHONY: bench
BENCH ?= .
bench:
	@$(foreach pkg,$(PKGS),go test -bench=$(BENCH) -run="^$$" $(BENCH_FLAGS) $(pkg);)
