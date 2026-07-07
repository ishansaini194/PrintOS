package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/render"
	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Placeholder per-page rates in paise (₹2 mono, ₹10 color). Business will tune
// these later — keep every rate in this one block.
const (
	rateMonoPaise  = 200
	rateColorPaise = 1000
)

// ratePaise returns the per-page rate for a job type.
func ratePaise(jobType string) int {
	if jobType == string(protocol.ColorColor) {
		return rateColorPaise
	}
	return rateMonoPaise
}

// PDFPath returns the on-disk path for a job's stored PDF.
func PDFPath(jobID string) string {
	return filepath.Join(pdfDir(), jobID+".pdf")
}

// pdfDir is where cleaned job PDFs are stored (env PRINTOS_PDF_DIR).
func pdfDir() string {
	if v := os.Getenv("PRINTOS_PDF_DIR"); v != "" {
		return v
	}
	return "uploads/jobs"
}

// Upload accepts a customer's file for a shop, normalizes it to a clean PDF,
// stores it, and creates a job awaiting payment. It returns the price and the
// claim code — NOTHING is sent to the agent until payment is confirmed
// (POST /pay/confirm).
func (h *Handlers) Upload(c *fiber.Ctx) error {
	shopID := c.FormValue("shop_id")

	// bad logs the exact reason a 400 is returned (with whatever shop/file
	// context is known so far) before replying, so a failed upload leaves a
	// clear line in the cloud terminal.
	var filename string
	bad := func(reason string) error {
		log.Printf("/upload 400: %s (shop=%s, file=%s)", reason, shopID, filename)
		return badRequest(c, reason)
	}

	if shopID == "" {
		return bad("shop_id required")
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return bad("file required")
	}
	filename = fileHeader.Filename
	if fileHeader.Size > render.MaxUploadBytes {
		return bad("file too large")
	}

	active, err := h.shops.IsActive(shopID)
	if err != nil || !active {
		return bad("unknown or inactive shop")
	}

	settings := parseSettings(c)

	// Save the upload to a temp file, preserving its extension so render can
	// detect the type.
	workDir, err := os.MkdirTemp("", "printos-upload-*")
	if err != nil {
		return serverError(c, "could not stage upload")
	}
	defer os.RemoveAll(workDir)
	tmpPath := filepath.Join(workDir, "upload"+filepath.Ext(fileHeader.Filename))
	if err := c.SaveFile(fileHeader, tmpPath); err != nil {
		return serverError(c, "could not save upload")
	}

	// Normalize to a clean, optimized PDF.
	cleanPDF, cleanup, err := render.Normalize(tmpPath)
	if err != nil {
		switch {
		case errors.Is(err, render.ErrTooLarge):
			return bad("file too large")
		case errors.Is(err, render.ErrUnsupported):
			return bad("unsupported file type")
		default:
			return bad(fmt.Sprintf("normalize failed: %v", err))
		}
	}
	defer cleanup()

	// Price from the normalized PDF: pages × copies × per-page rate.
	pages, err := render.PageCount(cleanPDF)
	if err != nil {
		return serverError(c, "could not count pages")
	}
	jobType := string(settings.Color)
	amountPaise := pages * settings.Copies * ratePaise(jobType)

	// The claim code must be unambiguous among the shop's active jobs.
	claimCode, err := h.uniqueClaimCode(shopID)
	if err != nil {
		return serverError(c, "could not allocate claim code")
	}

	// Create the job row (awaiting payment), then persist the PDF as
	// <job_id>.pdf and record its checksum for the eventual agent push.
	expires := time.Now().Add(holdTTL()).UTC()
	job, err := h.jobs.Create(store.NewJob{
		ShopID:         shopID,
		IdempotencyKey: genIdempotencyKey(),
		ClaimCode:      claimCode,
		Type:           jobType,
		Copies:         settings.Copies,
		Pages:          pages,
		AmountPaise:    amountPaise,
		Duplex:         settings.Duplex,
		PaperSize:      settings.PaperSize,
		ExpiresAt:      expires,
	})
	if err != nil {
		return serverError(c, "could not create job")
	}

	sha, err := storePDF(cleanPDF, PDFPath(job.ID))
	if err != nil {
		return serverError(c, "could not store pdf")
	}
	if err := h.jobs.SetSHA(job.ID, sha); err != nil {
		return serverError(c, "could not record pdf checksum")
	}

	return c.JSON(fiber.Map{
		"job_id":       job.ID,
		"amount_paise": amountPaise, // pages × copies × rate, in paise
		"claim_code":   job.ClaimCode,
	})
}

// parseSettings reads print options from the multipart form, with defaults.
// The printer type comes from "type" ("mono"/"color"); the older "color" field
// is still honored for compatibility.
func parseSettings(c *fiber.Ctx) protocol.PrintSettings {
	color := protocol.ColorMono
	if c.FormValue("type") == string(protocol.ColorColor) ||
		c.FormValue("color") == string(protocol.ColorColor) {
		color = protocol.ColorColor
	}
	copies, err := strconv.Atoi(c.FormValue("copies"))
	if err != nil || copies < 1 {
		copies = 1
	}
	paper := c.FormValue("paper_size")
	if paper == "" {
		paper = "A4"
	}
	return protocol.PrintSettings{
		Color:     color,
		Copies:    copies,
		Duplex:    c.FormValue("duplex") == "true",
		PaperSize: paper,
	}
}

// uniqueClaimCode generates a 6-digit code no active job of the shop is using,
// regenerating on collision.
func (h *Handlers) uniqueClaimCode(shopID string) (string, error) {
	for range 20 {
		code := genClaimCode()
		active, err := h.jobs.ClaimCodeActive(shopID, code)
		if err != nil {
			return "", err
		}
		if !active {
			return code, nil
		}
	}
	return "", fmt.Errorf("could not find a free claim code for shop %s", shopID)
}

// storePDF copies src to dest (creating the dir) and returns the sha256 of the
// stored bytes.
func storePDF(src, dest string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// genClaimCode returns a random 6-digit numeric claim code — the code the
// customer pays with and later types at the shop to release the print.
func genClaimCode() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = '0' + b[i]%10
	}
	return string(b)
}

// genIdempotencyKey returns a random idempotency key.
func genIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func badRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}

func serverError(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": msg})
}
