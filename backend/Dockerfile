# --- build stage ---
FROM golang:1.25-alpine AS build
WORKDIR /src

# 의존성 레이어 캐시
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- runtime stage ---
# distroless static: 셸·패키지 매니저 없음, 정적 바이너리 전용. 폐쇄망 배포에 적합.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/server"]
