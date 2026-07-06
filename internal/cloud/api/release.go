package api

import (
	"encoding/json"
	"errors"
	"html/template"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Release accepts the 6-digit claim code typed on the shop's release page,
// finds the matching paid/held job, and tells the agent to print it. Unpaid or
// unknown codes never print anything.
func (h *Handlers) Release(c *fiber.Ctx) error {
	var body struct {
		ShopID string `json:"shop_id"`
		Code   string `json:"code"`
	}
	if err := c.BodyParser(&body); err != nil || body.ShopID == "" || body.Code == "" {
		return badRequest(c, "shop_id and code required")
	}

	job, err := h.jobs.FindReleasable(body.ShopID, body.Code)
	if errors.Is(err, store.ErrNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no job found for that code"})
	}
	if err != nil {
		return serverError(c, "could not look up job")
	}

	payload, _ := json.Marshal(protocol.ReleaseMsg{JobID: job.ID})
	err = PushToAgent(job.ShopID, protocol.Envelope{
		Type:            protocol.MsgRelease,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "shop printer is offline, try again shortly",
		})
	}

	return c.JSON(fiber.Map{"job_id": job.ID, "status": "releasing"})
}

// releasePage is the minimal cloud-served page the shop PC keeps open: a code
// box that posts to /release. Polished UI comes later in the web repo.
var releasePage = template.Must(template.New("release").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PrintOS — Release a print</title>
<style>
  body { font-family: system-ui, sans-serif; display: grid; place-items: center; min-height: 90vh; }
  main { text-align: center; }
  input { font-size: 2rem; width: 8ch; text-align: center; letter-spacing: .3ch; }
  button { font-size: 1.2rem; margin-top: 1rem; padding: .4rem 1.5rem; }
  #result { margin-top: 1rem; min-height: 1.5rem; font-size: 1.1rem; }
  .ok { color: green; } .err { color: crimson; }
</style>
</head>
<body>
<main>
  <h1>Enter claim code</h1>
  <form id="f">
    <input id="code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autofocus autocomplete="off" required>
    <br>
    <button type="submit">Print</button>
  </form>
  <p id="result"></p>
</main>
<script>
const shopID = {{.ShopID}};
document.getElementById("f").addEventListener("submit", async (e) => {
  e.preventDefault();
  const result = document.getElementById("result");
  const input = document.getElementById("code");
  result.textContent = "…";
  result.className = "";
  try {
    const resp = await fetch("/release", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ shop_id: shopID, code: input.value }),
    });
    const data = await resp.json();
    if (resp.ok) {
      result.textContent = "Printing job " + data.job_id.slice(0, 8) + "…";
      result.className = "ok";
      input.value = "";
    } else {
      result.textContent = data.error || "Something went wrong.";
      result.className = "err";
    }
  } catch {
    result.textContent = "Could not reach the server.";
    result.className = "err";
  }
  input.focus();
});
</script>
</body>
</html>`))

// ReleasePage serves the code-entry page for one shop; the shop id rides in the
// URL so the page knows which shop it is releasing for.
func (h *Handlers) ReleasePage(c *fiber.Ctx) error {
	c.Type("html")
	return releasePage.Execute(c.Response().BodyWriter(), fiber.Map{
		"ShopID": c.Params("shop_id"),
	})
}
