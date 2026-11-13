package social

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/oauth"
)

var (
	ErrUnknownProvider = errorx.Define(errorx.NotFound, "social.unknown_provider")
	ErrNotLinked       = errorx.Define(errorx.PermissionDenied, "social.not_linked")
	ErrAlreadyLinked   = errorx.Define(errorx.Conflict, "social.already_linked")
)

// Client 是在第三方开放平台登记的应用。
type Client struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type Config struct {
	Google Client `yaml:"google"`
	GitHub Client `yaml:"github"`
	// RedirectURL 是单页应用中回调页的地址，{provider} 换成提供方的名称。
	RedirectURL string `yaml:"redirect_url"`
	// ClientRedirectURLs 是 App 登记的回调地址，Exchange 只接受这些地址。
	ClientRedirectURLs []string `yaml:"client_redirect_urls"`
	// GoogleAppClientIDs 是 App 原生登录使用的 Google client ID，例如 iOS 和 Android 各一个。
	GoogleAppClientIDs []string `yaml:"google_app_client_ids"`
}

// Providers 按名称创建全部提供方。Google 在这里读取发现文档，所以应当在启动时调用一次。
func Providers(ctx context.Context, cfg Config) (map[string]*oauth.Provider, error) {
	google, err := oauth.Discover(ctx, oauth.Config{
		Provider:           "google",
		Issuer:             "https://accounts.google.com",
		ClientID:           cfg.Google.ClientID,
		ClientSecret:       cfg.Google.ClientSecret,
		RedirectURL:        strings.ReplaceAll(cfg.RedirectURL, "{provider}", "google"),
		ClientRedirectURLs: cfg.ClientRedirectURLs,
		Audiences:          append([]string{cfg.Google.ClientID}, cfg.GoogleAppClientIDs...),
	})
	if err != nil {
		return nil, err
	}
	gh, err := oauth.New(oauth.Config{
		Provider:           "github",
		ClientID:           cfg.GitHub.ClientID,
		ClientSecret:       cfg.GitHub.ClientSecret,
		RedirectURL:        strings.ReplaceAll(cfg.RedirectURL, "{provider}", "github"),
		ClientRedirectURLs: cfg.ClientRedirectURLs,
		Scopes:             []string{"read:user", "user:email"},
	}, github.Endpoint, githubAccount)
	if err != nil {
		return nil, err
	}
	return map[string]*oauth.Provider{"google": google, "github": gh}, nil
}

// githubAccount 用 GitHub API 读取账号：用户 ID 来自 /user，邮箱和它是否已验证来自 /user/emails。
func githubAccount(ctx context.Context, client *http.Client, _ *oauth2.Token) (login.External, error) {
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", &u); err != nil {
		return login.External{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
		return login.External{}, err
	}
	ext := login.External{Subject: strconv.FormatInt(u.ID, 10), Name: cmp.Or(u.Name, u.Login)}
	for _, e := range emails {
		if e.Primary {
			ext.Email, ext.EmailVerified = e.Email, e.Verified
		}
	}
	return ext, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
