# === 第一階段：建置階段 ===
FROM golang:1.24-alpine AS builder

# 安裝 git 讓 go mod 正常下載
RUN apk add --no-cache git

# 設定工作目錄
WORKDIR /app

# 先複製 go.mod / go.sum 並下載依賴
COPY go.mod go.sum ./
RUN go mod download

# 複製所有程式碼
COPY . .

# 建立執行檔，禁用 CGO 減少依賴
RUN CGO_ENABLED=0 GOOS=linux go build -o server main.go

# === 第二階段：運行階段 ===
FROM alpine:3.19

WORKDIR /root/

# 從 builder 複製執行檔
COPY --from=builder /app/server .
# 複製前端靜態檔案
COPY static ./static

# 開放伺服器埠口
EXPOSE 7000

# 容器啟動時執行
CMD ["./server"]
