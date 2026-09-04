# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/serving .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/serving /serving

EXPOSE 8002
USER nonroot:nonroot
ENTRYPOINT ["/serving"]
