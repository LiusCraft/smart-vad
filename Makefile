UNAME := $(shell uname -s)

ifeq ($(UNAME),Darwin)
BREW_PREFIX := $(shell brew --prefix)
CGO_FLAGS := CGO_CFLAGS="-I$(BREW_PREFIX)/include/onnxruntime" CGO_LDFLAGS="-L$(BREW_PREFIX)/lib"
endif

GO := $(CGO_FLAGS) go

.PHONY: all setup build build-server build-cli test clean

setup:
	@bash scripts/setup.sh

all: build

build: build-server build-cli

build-server:
	$(GO) build -o server ./cmd/server

build-cli:
	$(GO) build -o cli ./cmd/cli

run-server: build-server
	./server -model silero_vad.onnx

test:
	$(GO) test ./...

clean:
	rm -f server cli
