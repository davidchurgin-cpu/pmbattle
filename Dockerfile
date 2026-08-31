FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run check && npm run build

FROM golang:1.26.5-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pmbattle .

FROM alpine:3.22
RUN addgroup -S pmbattle && adduser -S pmbattle -G pmbattle
WORKDIR /app
COPY --from=server /out/pmbattle /app/pmbattle
USER pmbattle
EXPOSE 8080
ENTRYPOINT ["/app/pmbattle"]

