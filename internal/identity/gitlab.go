package identity // import "github.com/pomerium/pomerium/internal/identity"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	oidc "github.com/coreos/go-oidc"
	"golang.org/x/oauth2"

	"github.com/pomerium/pomerium/internal/httputil"
	"github.com/pomerium/pomerium/internal/log"
	"github.com/pomerium/pomerium/internal/sessions"
	"github.com/pomerium/pomerium/internal/version"
)

const (
	defaultGitLabProviderURL = "https://gitlab.com"
	revokeURL                = "https://gitlab.com/oauth/revoke"
	defaultGitLabGroupURL    = "https://gitlab.com/api/v4/groups"
)

// GitLabProvider is an implementation of the OAuth Provider
type GitLabProvider struct {
	*Provider
	RevokeURL string `json:"revocation_endpoint"`
}

// NewGitLabProvider returns a new GitLabProvider.
// https://www.pomerium.io/docs/identity-providers/gitlab.html
func NewGitLabProvider(p *Provider) (*GitLabProvider, error) {
	ctx := context.Background()

	if p.ProviderURL == "" {
		p.ProviderURL = defaultGitLabProviderURL
	}

	var err error
	p.provider, err = oidc.NewProvider(ctx, p.ProviderURL)
	if err != nil {
		return nil, err
	}

	if len(p.Scopes) == 0 {
		p.Scopes = []string{oidc.ScopeOpenID, "api", "read_user", "profile", "email"}
	}

	p.verifier = p.provider.Verifier(&oidc.Config{ClientID: p.ClientID})
	p.oauth = &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     p.provider.Endpoint(),
		RedirectURL:  p.RedirectURL.String(),
		Scopes:       p.Scopes,
	}
	gp := &GitLabProvider{
		Provider:  p,
		RevokeURL: revokeURL,
	}

	if err := p.provider.Claims(&gp); err != nil {
		return nil, err
	}
	gp.UserGroupFn = gp.UserGroups
	return gp, nil
}

// UserGroups returns a slice of groups for the user.
//
// By default, this request returns 20 results at a time because the API results are paginated.
// https://docs.gitlab.com/ee/api/groups.html#list-groups
func (p *GitLabProvider) UserGroups(ctx context.Context, s *sessions.State) ([]string, error) {
	if s == nil || s.AccessToken == nil {
		return nil, errors.New("identity/gitlab: user session cannot be empty")
	}

	var response []struct {
		ID                             json.Number `json:"id"`
		Name                           string      `json:"name,omitempty"`
		Path                           string      `json:"path,omitempty"`
		Description                    string      `json:"description,omitempty"`
		Visibility                     string      `json:"visibility,omitempty"`
		ShareWithGroupLock             bool        `json:"share_with_group_lock,omitempty"`
		RequireTwoFactorAuthentication bool        `json:"require_two_factor_authentication,omitempty"`
		SubgroupCreationLevel          string      `json:"subgroup_creation_level,omitempty"`
		FullName                       string      `json:"full_name,omitempty"`
		FullPath                       string      `json:"full_path,omitempty"`
	}
	headers := map[string]string{"Authorization": fmt.Sprintf("Bearer %s", s.AccessToken.AccessToken)}
	err := httputil.Client(ctx, http.MethodGet, defaultGitLabGroupURL, version.UserAgent(), headers, nil, &response)
	if err != nil {
		return nil, err
	}

	var groups []string
	log.Debug().Interface("response", response).Msg("identity/gitlab: groups")

	for _, group := range response {
		groups = append(groups, group.ID.String())
	}

	return groups, nil
}

// Revoke attempts to revoke session access via revocation endpoint
// https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html#revoking-a-personal-access-token
func (p *GitLabProvider) Revoke(ctx context.Context, token *oauth2.Token) error {
	params := url.Values{}
	params.Add("access_token", token.AccessToken)

	err := httputil.Client(ctx, http.MethodPost, p.RevokeURL, version.UserAgent(), nil, params, nil)
	if err != nil && err != httputil.ErrTokenRevoked {
		return err
	}

	return nil
}
