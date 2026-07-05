package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/render"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// jobTTL is how long a created job stays valid before expiring.
const jobTTL = 2 * time.Hour

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
// stores it, creates a job, and pushes the job to the shop's agent.
func (h *Handlers) Upload(c *fiber.Ctx) error {
	shopID := c.FormValue("shop_id")
	if shopID == "" {
		return badRequest(c, "shop_id required")
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return badRequest(c, "file required")
	}
	if fileHeader.Size > render.MaxUploadBytes {
		return badRequest(c, "file too large")
	}

	active, err := h.shops.IsActive(shopID)
	if err != nil || !active {
		return badRequest(c, "unknown or inactive shop")
	}

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
			return badRequest(c, "file too large")
		case errors.Is(err, render.ErrUnsupported):
			return badRequest(c, "unsupported file type")
		default:
			return badRequest(c, "could not process file")
		}
	}
	defer cleanup()

	// Create the job row (state 'created'), then persist the PDF as <job_id>.pdf.
	expires := time.Now().Add(jobTTL).UTC()
	job, err := h.jobs.Create(shopID, genIdempotencyKey(), genClaimCode(), expires)
	if err != nil {
		return serverError(c, "could not create job")
	}

	sha, err := storePDF(cleanPDF, PDFPath(job.ID))
	if err != nil {
		return serverError(c, "could not store pdf")
	}

	// Build and push the job to the shop's agent.
	pj := protocol.Job{
		ID:             job.ID,
		ShopID:         shopID,
		IdempotencyKey: job.IdempotencyKey,
		Mode:           protocol.ModePrintNow,
		ClaimCode:      job.ClaimCode,
		PDFURL:         publicURL() + "/jobs/" + job.ID + "/pdf",
		PDFSHA256:      sha,
		Settings:       parseSettings(c),
		CreatedAt:      job.CreatedAt.UTC(),
		ExpiresAt:      expires,
	}
	payload, _ := json.Marshal(protocol.JobPushMsg{Job: pj})
	pushErr := PushToAgent(shopID, protocol.Envelope{
		Type:            protocol.MsgJobPush,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
	if pushErr != nil {
		// Shop offline: keep the job row; the agent can be re-sent later.
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"job_id":     job.ID,
			"claim_code": job.ClaimCode,
			"note":       "shop offline, will retry",
		})
	}

	return c.JSON(fiber.Map{"job_id": job.ID, "claim_code": job.ClaimCode})
}

// parseSettings reads print options from the multipart form, with defaults.
func parseSettings(c *fiber.Ctx) protocol.PrintSettings {
	color := protocol.ColorMono
	if c.FormValue("color") == string(protocol.ColorColor) {
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

// genClaimCode returns a short human-readable claim code (6 chars).
func genClaimCode() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = setupCodeAlphabet[int(b[i])%len(setupCodeAlphabet)]
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
