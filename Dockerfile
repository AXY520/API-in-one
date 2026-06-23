FROM node:20-alpine AS frontend
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend /app/dist ./web/dist
COPY . .
RUN CGO_ENABLED=0 go build -o api-in-one .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /app/api-in-one .
COPY --from=backend /app/web/dist ./web/dist
EXPOSE 3000
CMD ["./api-in-one"]
