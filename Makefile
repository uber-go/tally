BENCH_FLAGS ?= -cpuprofile=cpu.pprof -memprofile=mem.pprof -benchmem
PKGS ?= $(shell go list ./...)
PKG_FILES ?= $(shell find . -type f -name '*.go' |egrep -v '^./(thirdparty/|vendor/)')
LINT_IGNORE = (/m3/thrift/|/thirdparty/)
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
.SILENT: lint
lint:
	rm -rf lint.log
	echo "Checking formatting..."
	gofmt -l -s $(PKG_FILES) 2>&1 | egrep -v '$(LINT_IGNORE)' | tee -a lint.log
	echo "Checking lint..."
	$(foreach dir,$(PKGS),golint $(dir) 2>&1 | egrep -v '$(LINT_IGNORE)' | tee -a lint.log;)
	echo "Checking for unresolved FIXMEs..."
	git grep -i fixme | grep -v -e vendor -e Makefile | egrep -v '$(LINT_IGNORE)' | tee -a lint.log
	echo "Checking for license headers..."
	./check_license.sh | grep -v '$(LICENSE_IGNORE)' | tee -a lint.log
	[ ! -s lint.log ]

.PHONY: test
test:
	$(GO) test -timeout 1m -race -v ./...

.PHONY: examples
.SILENT: examples
examples:
	mkdir -p ./bin
	$(GO) build -o ./bin/print_example ./example/
	$(GO) build -o ./bin/m3_example ./m3/example/
	$(GO) build -o ./bin/prometheus_example ./prometheus/example/
	$(GO) build -o ./bin/statsd_example ./statsd/example/

.PHONY: cover
cover:
	$(GO) test -timeout 1m -cover -coverprofile cover.out -race -v ./...

.PHONY: coveralls
coveralls:
	goveralls -service=travis-ci || echo "Coveralls failed"

.PHONY: bench
.SILENT: bench
BENCH ?= .
bench:
	$(foreach pkg,$(PKGS),go test -bench=$(BENCH) -run="^$$" $(BENCH_FLAGS) $(pkg);)
