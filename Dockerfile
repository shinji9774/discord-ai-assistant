FROM golang:1.27.4-alpine3.24 AS build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

FROM build-base AS build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags lambda.norpc -trimpath -o /out/ ./cmd/...

FROM build-base AS build-tools
COPY tools ./tools
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -o /out/ ./tools/...

FROM alpine:3.24 AS runtime-base

FROM runtime-base AS tools
COPY --from=build-tools /out/voiceprobe /

# 結合テスト専用。Lambda Runtime Interface Emulator を足す
FROM runtime-base AS it
COPY --from=build /out/ingress /out/worker /out/register /out/voicereporter /out/voicefailure /
COPY --from=public.ecr.aws/lambda/provided:al2023 /usr/local/bin/aws-lambda-rie /usr/local/bin/aws-lambda-rie

# discordコマンドの登録専用
FROM runtime-base AS register
COPY --from=build /out/register /
ENTRYPOINT ["/register"]

# 本番。最後のステージなので、--target なしの docker build はこれを作る
FROM runtime-base AS runtime
COPY --from=build /out/ingress /out/worker /out/voicereporter /out/voicefailure /
