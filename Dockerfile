# ---- Build Stage ----
FROM golang:1.21 AS builder

ARG TARGETARCH
ARG ONNX_VERSION=1.21.0

RUN case ${TARGETARCH} in \
      amd64) ONNX_ARCH="x64" ;; \
      arm64) ONNX_ARCH="aarch64" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" && exit 1 ;; \
    esac \
    && ONNX_TGZ="onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz" \
    && curl -sLO "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/${ONNX_TGZ}" \
    && tar xzf "${ONNX_TGZ}" \
    && cp -r "onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}/include/"* /usr/local/include/ \
    && cp -r "onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}/lib/"* /usr/local/lib/ \
    && rm -rf "onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}" "${ONNX_TGZ}"

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=1 \
    CGO_CFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib"

RUN go build -ldflags="-s -w" -o server ./cmd/server

# ---- Runtime Stage ----
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libstdc++6 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/lib/libonnxruntime.so* /usr/local/lib/
RUN ldconfig

COPY --from=builder /app/server /usr/local/bin/server
COPY silero_vad.onnx /app/silero_vad.onnx

WORKDIR /app

EXPOSE 8080

ENTRYPOINT ["server"]
CMD ["-model", "/app/silero_vad.onnx"]
