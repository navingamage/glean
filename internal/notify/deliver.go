package notify

import (
	"bytes"
	"fmt"
	"net/http"
	"net/smtp"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Config says where a digest should go. Everything is optional; with none of it
// set, glean prints to the terminal and nothing else happens, which is the right
// default for a program you have just installed.
type Config struct {
	Webhook string `json:"webhook,omitempty"`

	Email struct {
		To       string `json:"to,omitempty"`
		From     string `json:"from,omitempty"`
		Host     string `json:"host,omitempty"` // host:port
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	} `json:"email"`

	// Desktop posts a system notification. It is the only channel that reaches
	// someone who is at their machine and not reading email, which is most of
	// the time a seven-day warning matters.
	Desktop bool `json:"desktop"`
}

// Send delivers a digest over every configured channel, returning the errors it
// hit rather than stopping at the first. A broken SMTP password should not cost
// you the webhook.
func Send(cfg Config, d *Digest) []error {
	if d.Empty() {
		return nil
	}
	var errs []error
	body := d.Text()

	if cfg.Webhook != "" {
		if err := postWebhook(cfg.Webhook, d.Subject(), body); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.Email.To != "" && cfg.Email.Host != "" {
		if err := sendMail(cfg, d.Subject(), body); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.Desktop {
		if err := desktop(d.Subject(), summaryLine(d)); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func postWebhook(url, subject, body string) error {
	payload := fmt.Sprintf("%s\n\n%s", subject, body)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notify: webhook returned %s", resp.Status)
	}
	return nil
}

func sendMail(cfg Config, subject, body string) error {
	host, _, ok := strings.Cut(cfg.Email.Host, ":")
	if !ok {
		return fmt.Errorf("notify: email host %q needs a port", cfg.Email.Host)
	}
	from := cfg.Email.From
	if from == "" {
		from = cfg.Email.Username
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", cfg.Email.To)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Content-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(body)

	var auth smtp.Auth
	if cfg.Email.Username != "" {
		auth = smtp.PlainAuth("", cfg.Email.Username, cfg.Email.Password, host)
	}
	if err := smtp.SendMail(cfg.Email.Host, auth, from, []string{cfg.Email.To}, msg.Bytes()); err != nil {
		return fmt.Errorf("notify: email: %w", err)
	}
	return nil
}

// desktop posts a system notification. Only macOS is wired up, because that is
// what this runs on; elsewhere it is a no-op rather than an error, so a config
// copied between machines does not start failing.
func desktop(title, body string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	script := fmt.Sprintf("display notification %q with title %q", body, title)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("notify: desktop: %w", err)
	}
	return nil
}

func summaryLine(d *Digest) string {
	var parts []string
	if n := len(d.Matches); n > 0 {
		parts = append(parts, fmt.Sprintf("%d to claim", n))
	}
	if n := len(d.Closing); n > 0 {
		parts = append(parts, fmt.Sprintf("%d closing", n))
	}
	if n := len(d.Maybes); n > 0 {
		parts = append(parts, fmt.Sprintf("%d possible", n))
	}
	if len(parts) == 0 {
		return "nothing new"
	}
	return strings.Join(parts, ", ")
}
