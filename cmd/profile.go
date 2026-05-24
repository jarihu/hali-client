package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hali/internal/config"
	"hali/internal/crypto"
	"hali/internal/networking"
	"hali/internal/profiles"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	profilePostMaxAttempts = 4
	profilePostBaseDelay   = 300 * time.Millisecond
	profilePostMaxDelay    = 3 * time.Second
)

func httpPostJSON(url string, data []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < profilePostMaxAttempts; attempt++ {
		resp, err := http.Post(url, "application/json", bytes.NewReader(data))
		if err == nil {
			if !isRetryableHTTPStatus(resp.StatusCode) {
				return resp, nil
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if len(bytes.TrimSpace(body)) > 0 {
				lastErr = fmt.Errorf("backend returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
			} else {
				lastErr = fmt.Errorf("backend returned %s", resp.Status)
			}
		} else {
			lastErr = err
		}

		if attempt == profilePostMaxAttempts-1 {
			break
		}
		delay := time.Duration(math.Pow(2, float64(attempt))) * profilePostBaseDelay
		if delay > profilePostMaxDelay {
			delay = profilePostMaxDelay
		}
		time.Sleep(delay)
	}
	return nil, lastErr
}

func isRetryableHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage your publisher profile",
}

var profileCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or update your publisher profile",
	Long: `Create or update your publisher profile and submit it to the backend.

By default this command prompts for profile fields.

Automation:
  --display-name NAME    Required in --non-interactive mode
  --description TEXT
  --website URL
  --contact VALUE
  --non-interactive      Disable prompts globally

Examples:
  hali profile create
  hali profile create --display-name "Jane" --description "Model publisher" --website https://example.com --contact jane@example.com --non-interactive
  hali profile create --display-name "Jane" --non-interactive --json`,
	RunE: runProfileCreate,
}

var (
	profileDisplayNameFlag string
	profileDescriptionFlag string
	profileWebsiteFlag     string
	profileContactFlag     string
)

func configureProfileFlags() {
	profileCreateCmd.Flags().StringVar(&profileDisplayNameFlag, "display-name", "", "Set display name without interactive prompt")
	profileCreateCmd.Flags().StringVar(&profileDescriptionFlag, "description", "", "Set profile description without interactive prompt")
	profileCreateCmd.Flags().StringVar(&profileWebsiteFlag, "website", "", "Set website URL without interactive prompt")
	profileCreateCmd.Flags().StringVar(&profileContactFlag, "contact", "", "Set contact field without interactive prompt")
}

func runProfileCreate(cmd *cobra.Command, args []string) error {
	pubkey, err := config.LoadOrCreateNodePublicKeyHex()
	if err != nil {
		return fmt.Errorf("load node pubkey: %w", err)
	}
	fmt.Printf("Using publisher key: %s\n", pubkey)
	profilePath := filepath.Join(config.ServiceDataDir(), "profile.json")
	var prof profiles.Profile
	prof.PubKey = pubkey
	prof.Timestamp = time.Now().Unix()

	// Try to load existing signed profile for defaults.
	if data, err := os.ReadFile(profilePath); err == nil {
		var sp profiles.SignedProfile
		if err := json.Unmarshal(data, &sp); err == nil && sp.Profile.PubKey != "" {
			prof = sp.Profile
		} else {
			// Backward compatibility: allow plain profile.json without signature wrapper.
			_ = json.Unmarshal(data, &prof)
		}
	}
	// Always lock profile ownership to local key.
	prof.PubKey = pubkey
	if s := strings.TrimSpace(profileDisplayNameFlag); s != "" {
		prof.DisplayName = s
	}
	if s := strings.TrimSpace(profileDescriptionFlag); s != "" {
		prof.Description = s
	}
	if s := strings.TrimSpace(profileWebsiteFlag); s != "" {
		prof.Website = s
	}
	if s := strings.TrimSpace(profileContactFlag); s != "" {
		prof.Contact = s
	}

	if !nonInteractive {
		r := bufio.NewReader(os.Stdin)
		fmt.Printf("Display name [%s]: ", prof.DisplayName)
		s, _ := r.ReadString('\n')
		s = strings.TrimSpace(s)
		if s != "" {
			prof.DisplayName = s
		}
		fmt.Printf("Description [%s]: ", prof.Description)
		s, _ = r.ReadString('\n')
		s = strings.TrimSpace(s)
		if s != "" {
			prof.Description = s
		}
		fmt.Printf("Website [%s]: ", prof.Website)
		s, _ = r.ReadString('\n')
		s = strings.TrimSpace(s)
		if s != "" {
			prof.Website = s
		}
		fmt.Printf("Contact (email, optional) [%s]: ", prof.Contact)
		s, _ = r.ReadString('\n')
		s = strings.TrimSpace(s)
		if s != "" {
			prof.Contact = s
		}
	}

	prof.Timestamp = time.Now().Unix()
	if strings.TrimSpace(prof.DisplayName) == "" {
		if nonInteractive {
			return fmt.Errorf("display_name is required in non-interactive mode (use --display-name)")
		}
		return fmt.Errorf("display_name is required (enter a value at the prompt)")
	}

	canonical, err := crypto.Canonicalize(prof)
	if err != nil {
		return fmt.Errorf("canonicalize profile: %w", err)
	}
	hash := sha256.Sum256(canonical)
	sig, err := config.SignNodePayloadHex(hash[:])
	if err != nil {
		return fmt.Errorf("sign profile: %w", err)
	}
	sp := profiles.SignedProfile{Profile: prof, Signature: sig}

	// Save locally
	if f, err := os.Create(profilePath); err == nil {
		_ = json.NewEncoder(f).Encode(sp)
		_ = f.Close()
	}

	// Submit to backend
	allowOverride, guardErr := shouldAllowUnreachablePublish(
		networking.PublishReachabilityPolicy{RequiresInternetReachability: true},
		"profile publish",
	)
	if guardErr != nil {
		return guardErr
	}
	if err := submitProfile(sp); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to submit profile: %v\n", err)
	} else if allowOverride {
		fmt.Println("Profile submitted successfully (unreachable publish override active).")
	} else {
		fmt.Println("Profile submitted successfully.")
	}
	return nil
}

func validateProfileBackendURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid profile backend URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid profile backend URL %q: scheme must be http or https", raw)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("invalid profile backend URL %q: host is required", raw)
	}
	return nil
}

func submitProfile(sp profiles.SignedProfile) error {
	backend := strings.TrimSpace(os.Getenv("HALI_PROFILE_BACKEND"))
	if backend == "" {
		backend = "http://127.0.0.1:3000/profile"
	}
	if err := validateProfileBackendURL(backend); err != nil {
		return err
	}
	data, _ := json.Marshal(sp)
	resp, err := httpPostJSON(backend, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		if len(bytes.TrimSpace(body)) > 0 {
			return fmt.Errorf("backend returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("backend returned %s", resp.Status)
	}
	return nil
}
