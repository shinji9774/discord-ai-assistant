IMAGE ?= discord-ai-assistant:latest
REGISTER_IMAGE ?= discord-ai-assistant:register
IT_IMAGE ?= discord-ai-assistant:it

# bot トークンを持つ SSM パラメータ（SecureString）のリージョンと名前
AWS_REGION ?= ap-northeast-1
BOT_TOKEN_PARAMETER_NAME ?= /discord-ai-assistant/bot-token

# go.mod の go 行から golang イメージのバージョンを決める
GO_VERSION := $(shell awk '$$1 == "go" { print $$2 }' go.mod)
GO_IMAGE ?= golang:$(GO_VERSION)

# golang コンテナ内の作業ディレクトリとキャッシュ。キャッシュは名前付きボリュームで持ち回る
CONTAINER_WORKDIR := /src
CONTAINER_CACHE_DIR := /cache
GO_CACHE_VOLUME ?= discord-ai-assistant-go-cache
DOCKER_SOCKET := /var/run/docker.sock
# --network host のコンテナからは、兄弟コンテナの公開ポートに localhost で届く
TESTCONTAINERS_HOST := localhost
DOCKER_SOCKET_GID := $(shell stat -c %g $(DOCKER_SOCKET) 2>/dev/null)

# ホストの uid/gid で動かし、リポジトリに root 所有のファイルを残さない
DOCKER_GO_RUN = docker run --rm \
	--user $(shell id -u):$(shell id -g) \
	--group-add $(DOCKER_SOCKET_GID) \
	--network host \
	-v $(CURDIR):$(CONTAINER_WORKDIR) -w $(CONTAINER_WORKDIR) \
	-v $(GO_CACHE_VOLUME):$(CONTAINER_CACHE_DIR) \
	-v $(DOCKER_SOCKET):$(DOCKER_SOCKET) \
	-e HOME=$(CONTAINER_CACHE_DIR)/home \
	-e GOMODCACHE=$(CONTAINER_CACHE_DIR)/mod \
	-e GOCACHE=$(CONTAINER_CACHE_DIR)/build \
	-e IT_IMAGE=$(IT_IMAGE) \
	-e TESTCONTAINERS_HOST_OVERRIDE=$(TESTCONTAINERS_HOST) \
	$(GO_IMAGE)

.PHONY: help vet test image image-it test-it it docker-test docker-test-it docker-cache-volume register

.DEFAULT_GOAL := help
help:
	@grep -hE "^[a-zA-Z_-]+:.*?## .*$$" $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m: %s\n", $$1, $$2}'

vet: ## Run go vet
	go vet ./...

lint: ## Run actionlint
	go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/*.yml

test: ## Run go test
	go test -v ./...

image-register: ## Build docker image for register
	docker build --target register -t $(REGISTER_IMAGE) .

image-it: ## Build docker image for integration test
	docker build --target it -t $(IT_IMAGE) .

image-runtime: ## Build docker image for runtime
	docker build --target runtime -t $(IMAGE) .

image: image-runtime image-register image-it ## Build all docker images

# イメージは事前に image-it か別の手段で作っておく
test-it: ## Run integration test
	IT_IMAGE=$(IT_IMAGE) go test -v -tags integration -count=1 ./test/integration/...

it: image-it test-it ## Build docker image for integration test and run integration test

# 新規ボリュームは root 所有なので、ホストの uid/gid で書けるようにする
docker-cache-volume: ## Create docker volume for go cache and set ownership to current user
	docker run --rm --user 0 -v $(GO_CACHE_VOLUME):$(CONTAINER_CACHE_DIR) $(GO_IMAGE) \
		chown $(shell id -u):$(shell id -g) $(CONTAINER_CACHE_DIR)

docker-remove-cache-volume: ## Remove docker volume for go cache
	docker volume rm $(GO_CACHE_VOLUME) >/dev/null 2>&1 || true

docker-test: docker-cache-volume ## Run go vet and go test in docker container
	$(DOCKER_GO_RUN) make vet test

# イメージは事前に make image-it でホスト側に作っておく
docker-test-it: docker-cache-volume ## Run integration test in docker container
	$(DOCKER_GO_RUN) make test-it

# 秘密情報は -e 名前 で渡し、値をコマンドラインに展開しない
# AWS profile は aws CLI が読む AWS_PROFILE に任せる（make register AWS_PROFILE=xxx でも AWS_PROFILE=xxx make register でも効く）
# aws が失敗したら docker run へ進まないよう、代入と export を分ける
register: ## Register bot commands to Discord
	DISCORD_BOT_TOKEN=$$(aws ssm get-parameter --region $(AWS_REGION) --name $(BOT_TOKEN_PARAMETER_NAME) --with-decryption --query Parameter.Value --output text) && \
	export DISCORD_BOT_TOKEN && \
	docker run --rm -e DISCORD_BOT_TOKEN $(REGISTER_IMAGE)
