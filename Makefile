# remote-cc-adapter — top-level build.
#
# One Go binary: rca. The native interceptor is platform-specific (macOS
# interpose dylib / Linux seccomp supervisor); `make native` builds it and
# copies it into cmd/rca/embedded/ so `make go` embeds it in the binary.

BIN := bin
GO  := go

EMBED_DIR := cmd/rca/embedded

UNAME_S := $(shell uname -s)

.PHONY: all go native test clean fmt vet macos linux

all: native
	$(MAKE) go

## Build rca into ./bin (embeds whatever is in cmd/rca/embedded/)
go:
	@mkdir -p $(BIN)
	$(GO) build -o $(BIN)/rca ./cmd/rca

## Build the native interceptor for the host platform and stage it for embedding
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
	cp native/macos/rcc_interpose.dylib $(EMBED_DIR)/

linux:
	$(MAKE) -C native/linux
	cp native/linux/rcc_seccomp $(EMBED_DIR)/

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN)
	rm -f $(EMBED_DIR)/rcc_interpose.dylib $(EMBED_DIR)/rcc_seccomp
	-$(MAKE) -C native/macos clean
	-$(MAKE) -C native/linux clean
