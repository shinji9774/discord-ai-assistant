IMAGE ?= discord-ai-assistant:latest
REGISTER_IMAGE ?= discord-ai-assistant:register
IT_IMAGE ?= discord-ai-assistant:it

# bot トークンを持つ SSM パラメータ（SecureString）のリージョンと名前
AWS_REGION ?= ap-northeast-1
BOT_TOKEN_PARAMETER_NAME ?= /discord-ai-assistant/bot-token

# Go のバージョンの正は Dockerfile の build-base の golang タグ。golang イメージはそこから決める
DOCKERFILE ?= Dockerfile
DOCKERFILE_GO_IMAGE := $(shell awk '$$1 == "FROM" && $$NF == "build-base" { print $$2; exit }' $(DOCKERFILE))
GO_IMAGE ?= $(DOCKERFILE_GO_IMAGE)

# devcontainer の Go イメージ（mcr.microsoft.com/devcontainers/go:<variant>-X.Y-<os>）の minor が、Dockerfile の Go と一致するかを検査する
DEVCONTAINER_JSON ?= .devcontainer/devcontainer.json
DOCKERFILE_GO_MINOR = $(shell echo '$(DOCKERFILE_GO_IMAGE)' | sed -nE 's/^golang:([0-9]+\.[0-9]+)\.[0-9]+$$/\1/p')
DEVCONTAINER_IMAGE = $(shell sed -nE 's/^[[:space:]]*"image":[[:space:]]*"([^"]+)".*/\1/p' $(DEVCONTAINER_JSON))
DEVCONTAINER_GO_MINOR = $(shell echo '$(DEVCONTAINER_IMAGE)' | sed -nE 's|^mcr\.microsoft\.com/devcontainers/go:[0-9]+-([0-9]+\.[0-9]+)-[a-z]+$$|\1|p')

# actionlint の版。上げるときはこの行を書き換える
ACTIONLINT_VERSION ?= v1.7.12

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

.PHONY: help vet test image image-it test-it it docker-test docker-test-it docker-lint docker-cache-volume register check-devcontainer-go test-review-comment

.DEFAULT_GOAL := help
help:
	@grep -hE "^[a-zA-Z_-]+:.*?## .*$$" $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m: %s\n", $$1, $$2}'

vet: ## Run go vet
	go vet ./...

lint: ## Run actionlint
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml

test: ## Run go test
	go test -v ./...

image-register: ## Build docker image for register
	docker build --target register -t $(REGISTER_IMAGE) .

image-it: ## Build docker image for integration test
	docker build --target it -t $(IT_IMAGE) .

image-tools: ## Build docker image for tools
	docker build --target tools -t $(IMAGE)-tools .

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

docker-lint: docker-cache-volume ## Run actionlint in docker container
	$(DOCKER_GO_RUN) make lint

# イメージは事前に make image-it でホスト側に作っておく
docker-test-it: docker-cache-volume ## Run integration test in docker container
	$(DOCKER_GO_RUN) make test-it

check-devcontainer-go: ## Check devcontainer Go minor version matches Dockerfile build-base
	@if [ -z "$(DOCKERFILE_GO_MINOR)" ]; then \
		echo "Cannot read Go version from $(DOCKERFILE): expected 'FROM golang:X.Y.Z AS build-base', got '$(DOCKERFILE_GO_IMAGE)'."; \
		exit 1; \
	fi
	@if [ -z "$(DEVCONTAINER_GO_MINOR)" ]; then \
		echo "Cannot read Go version from $(DEVCONTAINER_JSON): expected image 'mcr.microsoft.com/devcontainers/go:<variant>-X.Y-<os>', got '$(DEVCONTAINER_IMAGE)'."; \
		exit 1; \
	fi
	@if [ "$(DEVCONTAINER_GO_MINOR)" != "$(DOCKERFILE_GO_MINOR)" ]; then \
		echo "devcontainer Go version ($(DEVCONTAINER_GO_MINOR)) does not match Dockerfile Go version ($(DOCKERFILE_GO_MINOR) from $(DOCKERFILE_GO_IMAGE))."; \
		echo "Update the image tag in $(DEVCONTAINER_JSON) to mcr.microsoft.com/devcontainers/go:<variant>-$(DOCKERFILE_GO_MINOR)-<os>."; \
		exit 1; \
	fi
	@echo "devcontainer Go version ($(DEVCONTAINER_GO_MINOR)) matches Dockerfile Go version ($(DOCKERFILE_GO_IMAGE))."

test-review-comment: ## Run golden tests for Dependabot review comment rendering
	./.github/dependabot-review/test-render-comment.sh

# 秘密情報は -e 名前 で渡し、値をコマンドラインに展開しない
# AWS profile は aws CLI が読む AWS_PROFILE に任せる（make register AWS_PROFILE=xxx でも AWS_PROFILE=xxx make register でも効く）
# aws が失敗したら docker run へ進まないよう、代入と export を分ける
register: ## Register bot commands to Discord
	@DISCORD_BOT_TOKEN=$$(aws ssm get-parameter --region $(AWS_REGION) --name $(BOT_TOKEN_PARAMETER_NAME) --with-decryption --query Parameter.Value --output text) && \
	export DISCORD_BOT_TOKEN && \
	docker run --rm -e DISCORD_BOT_TOKEN $(REGISTER_IMAGE)

voiceprobe: ## Run voiceprobe tool in docker container
	@DISCORD_BOT_TOKEN=$$(aws ssm get-parameter --region $(AWS_REGION) --name $(BOT_TOKEN_PARAMETER_NAME) --with-decryption --query Parameter.Value --output text) && \
	export DISCORD_BOT_TOKEN && \
	GUILD_ID=$$(read -p "Enter server ID: " input && echo $$input) && \
	export GUILD_ID && \
	CHANNEL_ID=$$(read -p "Enter channel ID: " input && echo $$input) && \
	export CHANNEL_ID && \
	OUT_DIR=$$(mktemp -d) && \
	export OUT_DIR && \
	echo "Output directory: $$OUT_DIR" && \
	docker run --rm --cpus=0.25 -v $$OUT_DIR:/out -e DISCORD_BOT_TOKEN -e GUILD_ID -e CHANNEL_ID -e OUT_DIR=/out -e PROBE_DURATION=2m $(IMAGE)-tools /voiceprobe