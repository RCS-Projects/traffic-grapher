# Build the browser bundle separately so the final image contains no Node.js.
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/traffic-grapher .

FROM alpine:3.22
RUN addgroup -S traffic && adduser -S -G traffic traffic \
    && mkdir /data && chown traffic:traffic /data
COPY --from=backend /out/traffic-grapher /usr/local/bin/traffic-grapher
USER traffic
ENV HOST=0.0.0.0 PORT=8080 CONFIG_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/traffic-grapher"]
