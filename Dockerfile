# ---- build ----
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=arm64 go build -trimpath -o /out/studium . \
	&& mkdir -p /out/data

# ---- runtime ----
FROM gcr.io/distroless/static:nonroot
COPY --from=build --chown=nonroot:nonroot /out/studium /studium
COPY --from=build --chown=nonroot:nonroot /out/data /data
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["/studium"]
