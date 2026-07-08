# remote-cc-adapter — top-level build.
#
# Go binaries build on macOS and Linux. The native interceptor is
# platform-specific: the macOS interpose dylib and the Linux seccomp supervisor.

BIN := bin
GO  := go

GOBINS := remote-cc-adapter rcc-executor rcc-spawn-proxy

UNAME_S := $(shell uname -s)

.PHONY: all go native test clean fmt vet macos linux

all: go native

## Build all Go binaries into ./bin
go:
	@mkdir -p $(BIN)
	@for c in $(GOBINS); do \
		echo "build $$c"; \
		$(GO) build -o $(BIN)/$$c ./cmd/$$c || exit 1; \
	done

## Build the native interceptor for the host platform
native:
ifeq ($(UNAME_S),Darwin)
	$(MAKE) macos
else ifeq ($(UNAME_S),Linux)
	$(MAKE) linux
else
	@echo "native interceptor unsupported on $(UNAME_S)"
endif

macos:
	$(MAKE) -C native/macos

linux:
	$(MAKE) -C native/linux

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN)
	-$(MAKE) -C native/macos clean
	-$(MAKE) -C native/linux clean
