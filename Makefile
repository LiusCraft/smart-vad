CGO_FLAGS := CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib"
GO := $(CGO_FLAGS) go

.PHONY: all init build build-server build-demo run-server run-demo test clean

PYTHON ?= python3

init:
	brew install onnxruntime
	$(PYTHON) -m pip install silero-vad
	@echo "=== silero-vad version ==="
	$(PYTHON) -m pip show silero-vad | grep -E "^(Name|Version):"
	cp $$($(PYTHON) -c "import silero_vad; import os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))") .
	@echo "=== done ==="

all: build

build: build-server build-demo

build-server:
	$(GO) build -o server ./cmd/server

build-demo:
	$(GO) build -o demo ./cmd/demo

run-server: build-server
	./server -model silero_vad.onnx

run-demo: build-demo
	./demo -input $(INPUT) -model silero_vad.onnx

test:
	$(GO) test ./...

clean:
	rm -f server demo
