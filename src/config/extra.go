package config

import (
	"SignTools/src/builders"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ExtraConf extends the upstream configuration without changing any legacy keys.
type ExtraConf struct {
	EncKey         string            `yaml:"enc_key,omitempty" json:"-"`
	Builders       []ExtraBuilder    `yaml:"builders,omitempty"`
	BuilderAliases map[string]string `yaml:"builder_aliases,omitempty"`
	DefaultBuilder string            `yaml:"default_builder,omitempty"`
	DefaultProfile string            `yaml:"default_profile,omitempty"`
	Repositories   []Repository      `yaml:"repositories,omitempty"`
	LoginPage      bool              `yaml:"login_page,omitempty"`
	WebPush        WebPush           `yaml:"webpush,omitempty"`
}

type WebPush struct {
	Enable     bool   `yaml:"enable,omitempty"`
	Subject    string `yaml:"subject,omitempty"`
	PublicKey  string `yaml:"public_key,omitempty"`
	PrivateKey string `yaml:"private_key,omitempty"`
}

type Repository struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url"`
}

// Credentials are accepted on writes and deliberately absent from BuilderView.
type ExtraBuilder struct {
	ID         string                  `yaml:"id" json:"id"`
	Name       string                  `yaml:"name" json:"name"`
	Type       string                  `yaml:"type" json:"type"`
	Enable     *bool                   `yaml:"enable,omitempty" json:"enable,omitempty"`
	GitHub     builders.GitHubData     `yaml:"github,omitempty" json:"github,omitempty"`
	Semaphore  builders.SemaphoreData  `yaml:"semaphore,omitempty" json:"semaphore,omitempty"`
	SelfHosted builders.SelfHostedData `yaml:"selfhosted,omitempty" json:"selfhosted,omitempty"`
}

type BuilderView struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Type    string `json:"Type"`
	Managed bool   `json:"Managed"`
}

var builderMu sync.RWMutex
var runtimeBuilders = map[string]ExtraBuilder{}
var builderIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (b ExtraBuilder) Make() (builders.Builder, error) {
	if !builderIDPattern.MatchString(b.ID) {
		return nil, fmt.Errorf("builder id must contain 1–64 letters, digits, hyphens or underscores")
	}
	switch strings.ToLower(b.Type) {
	case "github":
		d := b.GitHub
		if d.OrgName == "" || d.RepoName == "" || d.Token == "" {
			return nil, fmt.Errorf("GitHub owner, repository and token are required")
		}
		if d.WorkflowFileName == "" {
			d.WorkflowFileName = "sign.yml"
		}
		if d.Ref == "" {
			d.Ref = "master"
		}
		return builders.MakeGitHub(&d), nil
	case "selfhosted":
		d := b.SelfHosted
		u, err := url.Parse(d.Url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("self-hosted URL must be an HTTP or HTTPS URL without credentials")
		}
		if d.Key == "" {
			return nil, fmt.Errorf("self-hosted key is required")
		}
		return builders.MakeSelfHosted(&d), nil
	case "semaphore":
		d := b.Semaphore
		if d.OrgName == "" || d.ProjectName == "" || d.Token == "" {
			return nil, fmt.Errorf("Semaphore organization, project and token are required")
		}
		if d.Ref == "" {
			d.Ref = "refs/heads/master"
		}
		if d.SecretName == "" {
			d.SecretName = "ios-signer"
		}
		return builders.MakeSemaphore(&d), nil
	default:
		return nil, fmt.Errorf("builder type must be github, selfhosted or semaphore")
	}
}

func (f *File) makeBuilders() (map[string]builders.Builder, error) {
	result := f.Builder.MakeEnabled()
	loaded := map[string]ExtraBuilder{}
	for _, b := range f.ExtraConf.Builders {
		if b.Enable != nil && !*b.Enable {
			continue
		}
		if _, exists := result[b.ID]; exists {
			return nil, fmt.Errorf("duplicate builder id: %s", b.ID)
		}
		builder, err := b.Make()
		if err != nil {
			return nil, fmt.Errorf("builder %s: %w", b.ID, err)
		}
		result[b.ID] = builder
	}
	data, err := os.ReadFile(filepath.Join(f.SaveDir, "extra-builders.yml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var extra []ExtraBuilder
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &extra); err != nil {
			return nil, fmt.Errorf("read saved builders: %w", err)
		}
	}
	for _, b := range extra {
		if _, exists := result[b.ID]; exists {
			return nil, fmt.Errorf("duplicate saved builder id: %s", b.ID)
		}
		builder, err := b.Make()
		if err != nil {
			return nil, fmt.Errorf("saved builder %s: %w", b.ID, err)
		}
		result[b.ID], loaded[b.ID] = builder, b
	}
	builderMu.Lock()
	runtimeBuilders = loaded
	builderMu.Unlock()
	return result, nil
}

func GetBuilder(id string) (builders.Builder, bool) {
	builderMu.RLock()
	defer builderMu.RUnlock()
	b, ok := Current.Builder[id]
	return b, ok
}

func IsManagedBuilder(id string) bool {
	builderMu.RLock()
	defer builderMu.RUnlock()
	_, ok := runtimeBuilders[id]
	return ok
}

func BuilderSnapshot() map[string]builders.Builder {
	builderMu.RLock()
	defer builderMu.RUnlock()
	result := make(map[string]builders.Builder, len(Current.Builder))
	for id, b := range Current.Builder {
		result[id] = b
	}
	return result
}

func builderNameLocked(id string) string {
	if Current.File != nil {
		if name := Current.ExtraConf.BuilderAliases[id]; name != "" {
			return name
		}
		for _, b := range Current.ExtraConf.Builders {
			if b.ID == id && b.Name != "" {
				return b.Name
			}
		}
	}
	if b, ok := runtimeBuilders[id]; ok && b.Name != "" {
		return b.Name
	}
	return id
}

func BuilderName(id string) string {
	builderMu.RLock()
	defer builderMu.RUnlock()
	return builderNameLocked(id)
}

func BuilderMetadata() []BuilderView {
	builderMu.RLock()
	defer builderMu.RUnlock()
	result := make([]BuilderView, 0, len(Current.Builder))
	for id, b := range Current.Builder {
		kind := ""
		switch b.(type) {
		case *builders.GitHub:
			kind = "github"
		case *builders.SelfHosted:
			kind = "selfhosted"
		case *builders.Semaphore:
			kind = "semaphore"
		}
		_, managed := runtimeBuilders[id]
		result = append(result, BuilderView{ID: id, Name: builderNameLocked(id), Type: kind, Managed: managed})
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result
}

func saveRuntimeLocked(next map[string]ExtraBuilder) error {
	all := make([]ExtraBuilder, 0, len(next))
	for _, b := range next {
		all = append(all, b)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	data, err := yaml.Marshal(all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Current.SaveDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(Current.SaveDir, ".extra-builders-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(Current.SaveDir, "extra-builders.yml"))
}

func AddBuilder(b ExtraBuilder) error {
	b.ID, b.Name = strings.TrimSpace(b.ID), strings.TrimSpace(b.Name)
	builder, err := b.Make()
	if err != nil {
		return err
	}
	builderMu.Lock()
	defer builderMu.Unlock()
	if _, exists := Current.Builder[b.ID]; exists {
		return fmt.Errorf("builder id already exists")
	}
	next := make(map[string]ExtraBuilder, len(runtimeBuilders)+1)
	for id, v := range runtimeBuilders {
		next[id] = v
	}
	next[b.ID] = b
	if err := saveRuntimeLocked(next); err != nil {
		return fmt.Errorf("save builder: %w", err)
	}
	if Current.Builder == nil {
		Current.Builder = map[string]builders.Builder{}
	}
	Current.Builder[b.ID] = builder
	runtimeBuilders = next
	return nil
}

func RemoveBuilder(id string) error {
	builderMu.Lock()
	defer builderMu.Unlock()
	if _, ok := runtimeBuilders[id]; !ok {
		return fmt.Errorf("only builders added in the browser can be removed here")
	}
	if len(Current.Builder) <= 1 {
		return fmt.Errorf("at least one builder must remain")
	}
	next := make(map[string]ExtraBuilder, len(runtimeBuilders))
	for key, b := range runtimeBuilders {
		if key != id {
			next[key] = b
		}
	}
	if err := saveRuntimeLocked(next); err != nil {
		return fmt.Errorf("save builders: %w", err)
	}
	delete(Current.Builder, id)
	runtimeBuilders = next
	return nil
}

// DecodeBuilder rejects unknown fields so a mistyped token field is never silently ignored.
func DecodeBuilder(decoder *json.Decoder) (ExtraBuilder, error) {
	decoder.DisallowUnknownFields()
	var b ExtraBuilder
	err := decoder.Decode(&b)
	if err == nil {
		var trailing any
		if next := decoder.Decode(&trailing); next != io.EOF {
			err = fmt.Errorf("expected a single builder object")
		}
	}
	return b, err
}
