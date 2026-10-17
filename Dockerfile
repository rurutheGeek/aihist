# aihist のサーバー。各マシンへ配る実行ファイル（/api/bin）も同じイメージに入れる。
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -e; mkdir -p /out/bin; \
    for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64; do \
      os=${t%/*}; arch=${t#*/}; ext=; [ "$os" = windows ] && ext=.exe; \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "/out/bin/aihist-$os-$arch$ext" ./cmd/aihist; \
    done; \
    cp "/out/bin/aihist-$TARGETOS-$TARGETARCH" /out/aihist; \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/aihist /usr/local/bin/aihist
COPY --from=build /out/bin /opt/aihist/bin
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/aihist", "-db", "/data/aihist.db", "serve", "-addr", ":8080", "-raw", "/data/raw", "-bin", "/opt/aihist/bin"]
