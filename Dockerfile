# PrintOS cloud — multi-stage build.
# Stage 1 builds a static binary; stage 2 is an Ubuntu runtime carrying the
# document-normalization tools the cloud shells out to (soffice/convert/
# heif-convert) plus pdfinfo for page counting.

# ---- Stage 1: builder ----
# Base pinned to the go.mod version (go 1.25.0).
FROM golang:1.25 AS builder

WORKDIR /src

# Cache module downloads before copying the full tree.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build (CGO off) so the binary runs on the Ubuntu runtime without libc
# surprises.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/cloud ./cmd/cloud

# ---- Stage 2: runtime ----
FROM ubuntu:24.04

# Document-normalization tools (installed INSIDE the image — the cloud runs in
# this container and execs them; host installs do not count):
#   libreoffice       -> soffice     (docx/office -> pdf)
#   imagemagick       -> convert     (jpg/png -> pdf)
#   libheif-examples  -> heif-convert (heic -> jpg)
#   poppler-utils     -> pdfinfo     (page count)
# ca-certificates is needed for outbound HTTPS (Razorpay API).
RUN apt-get update && apt-get install -y --no-install-recommends \
        libreoffice \
        imagemagick \
        libheif-examples \
        libheif-plugin-libde265 \
        poppler-utils \
        ghostscript \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# --- Known follow-up: ImageMagick PDF policy ---
# Ubuntu's ImageMagick-6 ships a policy that can block PDF read/write, so a
# jpg/heic -> pdf `convert` may fail with "not authorized ... PDF". If the
# upload tests show that error, uncomment the line below and rebuild. Left off
# by default per the handoff (only enable if testing proves it's needed).
# RUN sed -i 's/rights="none" pattern="PDF"/rights="read|write" pattern="PDF"/' /etc/ImageMagick-6/policy.xml

WORKDIR /app

# The migration runner reads ./migrations relative to the working directory at
# startup, so the SQL files must ship in the image alongside the binary.
COPY --from=builder /out/cloud /app/cloud
COPY migrations /app/migrations

# Persistent PDF storage lives here (see PRINTOS_PDF_DIR); mounted as a volume
# by docker-compose.
RUN mkdir -p /data/pdfs

EXPOSE 8080

ENTRYPOINT ["/app/cloud"]
