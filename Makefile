# "go install"-ed binaries will be placed here during development.
export GOBIN ?= $(shell pwd)/bin

BENCH_FLAGS ?= -cpuprofile=cpu.pprof -memprofile=mem.pprof -benchmem
GO_FILES = $(shell find . \
	   '(' -path '*/.*' -o -path './thirdparty/*' -prune ')' -o \
	   '(' -type f -a -name '*.go' ')' -print)
MODULES = . ./tools

LINT_IGNORE = m3/thrift\|thirdparty
LICENSE_IGNORE = m3/thrift\|thirdparty

GOLINT = $(GOBIN)/golint

.PHONY: all
all: lint test

.PHONY: lint
lint: gofmt golint gomodtidy license

.PHONY: golint
golint: $(GOLINT)
	@echo "Checking lint..."
	@$(eval LOG := $(shell mktemp -t golint.XXXXX))
	@$(GOLINT) ./... | grep -v '$(LINT_IGNORE)' > $(LOG) || true
	@[ ! -s "$(LOG)" ] || \
		(echo "golint failed:" | \
		cat - $(LOG) && false)

$(GOLINT): tools/go.mod
	cd tools && go install golang.org/x/lint/golint

.PHONY: gofmt
gofmt:
	@echo "Checking formatting..."
	$(eval LOG := $(shell mktemp -t gofmt.XXXXX))
	@gofmt -e -s -l $(GO_FILES) | grep -v '$(LINT_IGNORE)' > $(LOG) || true
	@[ ! -s "$(LOG)" ] || \
		(echo "gofmt failed. Please reformat the following files:" | \
		cat - $(LOG) && false)


.PHONY: gomodtidy
gomodtidy: go.mod go.sum
	@echo "Checking go.mod and go.sum..."
	@$(foreach mod,$(MODULES),\
		(cd $(mod) && go mod tidy) &&) true
	@if ! git diff --quiet $^; then \
		echo "go mod tidy changed files:" && \
		git status --porcelain $^ && \
		false; \
	fi

.PHONY: license
license: check_license.sh
	@echo "Checking for license headers..."
	$(eval LOG := $(shell mktemp -t gofmt.XXXXX))
	@./check_license.sh | grep -v '$(LICENSE_IGNORE)' > $(LOG) || true
	@[ ! -s "$(LOG)" ] || \
		(echo "Missing license headers in some files:" | \
		cat - $(LOG) && false)

.PHONY: test
test:
	go test -race -v ./...

.PHONY: examples
examples:
	mkdir -p ./bin
	go build -o ./bin/print_example ./example/
	go build -o ./bin/m3_example ./m3/example/
	go build -o ./bin/prometheus_example ./prometheus/example/
	go build -o ./bin/statsd_example ./statsd/example/

.PHONY: cover
cover:
	go test -cover -coverprofile=cover.out -coverpkg=./... -race -v ./...
	go tool cover -html=cover.out -o cover.html

.PHONY: bench
BENCH ?= .
bench:
	go test -bench=$(BENCH) -run="^$$" $(BENCH_FLAGS) ./...

