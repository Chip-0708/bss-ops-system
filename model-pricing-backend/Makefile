# 模型管理与定价中心 — 本地开发常用命令
# Windows 下若 `make` 命令不存在，使用 `mingw32-make`，或执行：alias make=mingw32-make

DB_DSN ?= postgres://app:dev_only@127.0.0.1:5433/model_bss?sslmode=disable&TimeZone=Asia/Shanghai
MIG ?= migrations
BIN ?= bin/server

.PHONY: help pg-start pg-stop pg-restart pg-logs psql \
        migrate-new migrate-up migrate-up1 migrate-down migrate-down1 \
        migrate-force migrate-version migrate-drop \
        build run lint fmt test test-cover tidy audit dev

help:
	@echo "常用命令："
	@echo "  make pg-start        启动本地 PG 容器（5433）"
	@echo "  make psql            进入 psql 命令行"
	@echo "  make migrate-new n=init_schema   新建一个迁移文件"
	@echo "  make migrate-up      执行全部未应用的迁移"
	@echo "  make migrate-down1   回滚最近一个迁移（谨慎）"
	@echo "  make build           编译后端"
	@echo "  make dev             本地启动服务（开发模式，同 run）"
	@echo "  make run             本地启动服务"
	@echo "  make lint            静态检查"
	@echo "  make test            跑测试"

# ---------------- 本地 PostgreSQL（Docker，端口 5433） ----------------
pg-start:
	docker start model_bss_pg 2>/dev/null || \
	docker run -d --name model_bss_pg -e POSTGRES_DB=model_bss -e POSTGRES_USER=app \
	  -e POSTGRES_PASSWORD=dev_only -e TZ=Asia/Shanghai \
	  -p 5433:5432 -v model_bss_pgdata:/var/lib/postgresql/data postgres:15-alpine

pg-stop:
	docker stop model_bss_pg

pg-restart: pg-stop pg-start

pg-logs:
	docker logs -f model_bss_pg

psql:
	docker exec -it model_bss_pg psql -U app -d model_bss

# ---------------- 数据库迁移（唯一建表入口） ----------------
migrate-new:
	migrate create -ext sql -dir $(MIG) -seq $(n)

migrate-up:
	migrate -path $(MIG) -database "$(DB_DSN)" up

migrate-up1:
	migrate -path $(MIG) -database "$(DB_DSN)" up 1

migrate-down:
	migrate -path $(MIG) -database "$(DB_DSN)" down

migrate-down1:
	migrate -path $(MIG) -database "$(DB_DSN)" down 1

migrate-force:
	migrate -path $(MIG) -database "$(DB_DSN)" force $(v)

migrate-version:
	migrate -path $(MIG) -database "$(DB_DSN)" version

# 危险：清空 public schema（仅本地开发重建用）
migrate-drop:
	migrate -path $(MIG) -database "$(DB_DSN)" drop -f

# ---------------- 构建 / 检查 / 测试 ----------------
build:
	go build -o $(BIN) ./cmd/server

dev:
	go run ./cmd/server

run:
	go run ./cmd/server

lint:
	golangci-lint run ./...

swag:
	swag init -g cmd/server/main.go -o openapi --outputTypes json,yaml

fmt:
	go fmt ./...

test:
	go test ./... -count=1

test-cover:
	go test ./... -count=1 -coverprofile=coverage.txt -covermode=atomic

tidy:
	go mod tidy

audit:
	go vet ./...
	golangci-lint run ./...
	go test ./... -count=1
