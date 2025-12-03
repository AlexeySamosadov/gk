# Extract Geth binary from prebuilt container
FROM ghcr.io/product-science/bridge-geth:0.2.5@sha256:9216b070c7d63e1df65f88fd4f4a020826786571217da4ad502a93097b915abc AS geth-source

# Extract Prysm binaries from prebuilt container  
FROM ghcr.io/product-science/bridge-prysm:0.2.5@sha256:4855d3a8c1c211b5b8cb3620f37a3a8d80561bf2ab35e3698d9f32eccbe02d51 AS prysm-source

# Final image
FROM alpine:latest
# coreutils provides stdbuf for line-buffering Prysm logs
RUN apk add --no-cache ca-certificates openssl bash curl coreutils

# Copy Geth binary from prebuilt container
COPY --from=geth-source /usr/local/bin/geth /usr/local/bin/

# Copy Prysm binaries from prebuilt container
COPY --from=prysm-source /usr/local/bin/beacon-chain /usr/local/bin/
COPY --from=prysm-source /usr/local/bin/validator /usr/local/bin/

# Create necessary directories
RUN mkdir -p /data/geth /data/prysm /data/jwt

# Set environment variables
ENV GETH_DATA_DIR=/data/geth
ENV PRYSM_DATA_DIR=/data/prysm
ENV JWT_SECRET_PATH=/data/jwt/jwt.hex

# Copy startup script
COPY script.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/script.sh

ENTRYPOINT ["/usr/local/bin/script.sh"] 