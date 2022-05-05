package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-github/v42/github"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/h2non/gock.v1"

	"github.com/trufflesecurity/trufflehog/v3/pkg/pb/credentialspb"
	"github.com/trufflesecurity/trufflehog/v3/pkg/pb/sourcespb"
)

func createTestSource(src *sourcespb.GitHub) (*Source, *anypb.Any) {
	s := &Source{}
	conn, err := anypb.New(src)
	if err != nil {
		panic(err)
	}
	return s, conn
}

func initTestSource(src *sourcespb.GitHub) *Source {
	s, conn := createTestSource(src)
	if err := s.Init(context.TODO(), "test - github", 0, 1337, false, conn, 1); err != nil {
		panic(err)
	}
	return s
}

func TestInit(t *testing.T) {
	source, conn := createTestSource(&sourcespb.GitHub{
		Repositories: []string{"https://github.com/dustin-decker/secretsandstuff.git"},
		Credential: &sourcespb.GitHub_Token{
			Token: "super secret token",
		},
	})

	err := source.Init(context.TODO(), "test - github", 0, 1337, false, conn, 1)
	assert.Nil(t, err)

	// TODO: test error case
}

func TestAddReposByOrg(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/orgs/super-secret-org/repos").
		Reply(200).
		JSON([]map[string]string{{"clone_url": "super-secret-repo"}})

	s := initTestSource(nil)
	// gock works here because github.NewClient is using the default HTTP Transport
	err := s.addReposByOrg(context.TODO(), github.NewClient(nil), "super-secret-org")
	assert.Nil(t, err)
	assert.Equal(t, 1, len(s.repos))
	assert.Equal(t, []string{"super-secret-repo"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestAddReposByUser(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/users/super-secret-user/repos").
		Reply(200).
		JSON([]map[string]string{{"clone_url": "super-secret-repo"}})

	s := initTestSource(nil)
	err := s.addReposByUser(context.TODO(), github.NewClient(nil), "super-secret-user")
	assert.Nil(t, err)
	assert.Equal(t, 1, len(s.repos))
	assert.Equal(t, []string{"super-secret-repo"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestAddGistsByUser(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/users/super-secret-user/gists").
		Reply(200).
		JSON([]map[string]string{{"git_pull_url": "super-secret-gist"}})

	s := initTestSource(nil)
	s.addGistsByUser(context.TODO(), github.NewClient(nil), "super-secret-user")
	assert.Equal(t, 1, len(s.repos))
	assert.Equal(t, []string{"super-secret-gist"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestAddMembersByApp(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/app/installations").
		Reply(200).
		JSON([]map[string]interface{}{
			{"account": map[string]string{"login": "super-secret-org"}},
		})
	gock.New("https://api.github.com").
		Get("/orgs/super-secret-org/members").
		Reply(200).
		JSON([]map[string]interface{}{
			{"login": "ssm1"},
			{"login": "ssm2"},
			{"login": "ssm3"},
		})

	s := initTestSource(nil)
	err := s.addMembersByApp(context.TODO(), github.NewClient(nil), github.NewClient(nil))
	assert.Nil(t, err)
	assert.Equal(t, 3, len(s.members))
	assert.Equal(t, []string{"ssm1", "ssm2", "ssm3"}, s.members)
	assert.True(t, gock.IsDone())
}

func TestAddReposByApp(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/installation/repositories").
		Reply(200).
		JSON(map[string]interface{}{
			"repositories": []map[string]string{
				{"clone_url": "ssr1"},
				{"clone_url": "ssr2"},
			},
		})

	s := initTestSource(nil)
	err := s.addReposByApp(context.TODO(), github.NewClient(nil))
	assert.Nil(t, err)
	assert.Equal(t, 2, len(s.repos))
	assert.Equal(t, []string{"ssr1", "ssr2"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestAddOrgsByUser(t *testing.T) {
	defer gock.Off()

	// NOTE: addOrgsByUser calls /user/orgs to get the orgs of the
	// authenticated user
	gock.New("https://api.github.com").
		Get("/user/orgs").
		Reply(200).
		JSON([]map[string]interface{}{
			{"name": "sso1"},
			{"login": "sso2"},
		})

	s := initTestSource(nil)
	s.addOrgsByUser(context.TODO(), github.NewClient(nil), "super-secret-user")
	assert.Equal(t, 2, len(s.orgs))
	assert.Equal(t, []string{"sso1", "sso2"}, s.orgs)
	assert.True(t, gock.IsDone())
}

// TODO: normalizeRepos doesn't appear correct
func TestNormalizeRepos(t *testing.T) {
	defer gock.Off()

	s := initTestSource(nil)
	s.repos = []string{"https://github.com/super-secret-user/super-secret-repo"}
	s.normalizeRepos(context.TODO(), github.NewClient(nil))

	assert.Equal(t, 2, len(s.repos))
	assert.Equal(t, []string{
		"https://github.com/super-secret-user/super-secret-repo",
		"https://github.com/super-secret-user/super-secret-repo.git",
	}, s.repos)

	gock.New("https://api.github.com").
		Get("/users/super-secret-user/gists").
		Reply(200).
		JSON([]map[string]string{{"git_pull_url": "super-secret-gist"}})
	gock.New("https://api.github.com").
		Get("/users/super-secret-user/repos").
		Reply(200).
		JSON([]map[string]string{{"clone_url": "super-secret-repo"}})
	s.repos = []string{"super-secret-user"}
	s.normalizeRepos(context.TODO(), github.NewClient(nil))

	assert.Equal(t, 2, len(s.repos))
	assert.Equal(t, []string{
		"super-secret-repo",
		"super-secret-gist",
	}, s.repos)

	gock.New("https://api.github.com").
		Get("/users/not-found/gists").
		Reply(404)
	gock.New("https://api.github.com").
		Get("/users/not-found/repos").
		Reply(404)

	s.repos = []string{"not-found"}
	s.normalizeRepos(context.TODO(), github.NewClient(nil))
	assert.Equal(t, 2, len(s.repos))
	assert.Equal(t, []string{
		"not-found",
		"",
	}, s.repos)

	assert.True(t, gock.IsDone())
}

func TestHandleRateLimit(t *testing.T) {
	assert.False(t, handleRateLimit(nil, nil))

	err := &github.RateLimitError{}
	res := &github.Response{Response: &http.Response{Header: make(http.Header, 0)}}
	res.Header.Set("x-ratelimit-remaining", "0")
	res.Header.Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Unix()+1, 10))
	assert.True(t, handleRateLimit(err, res))
}

func TestEnumerateUnauthenticated(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/orgs/super-secret-org/repos").
		Reply(200).
		JSON([]map[string]string{{"clone_url": "super-secret-repo"}})

	s := initTestSource(nil)
	s.orgs = []string{"super-secret-org"}
	_ = s.enumerateUnauthenticated(context.TODO())
	assert.Equal(t, 1, len(s.repos))
	assert.Equal(t, []string{"super-secret-repo"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestEnumerateWithToken(t *testing.T) {
	defer gock.Off()

	gock.New("https://api.github.com").
		Get("/user").
		Reply(200).
		JSON(map[string]string{"login": "super-secret-user"})

	gock.New("https://api.github.com").
		Get("/users/super-secret-user/repos").
		Reply(200).
		JSON([]map[string]string{{"clone_url": "super-secret-repo"}})

	s := initTestSource(nil)
	_, err := s.enumerateWithToken(context.TODO(), "https://api.github.com", "token")
	assert.Nil(t, err)
	assert.Equal(t, 1, len(s.repos))
	assert.Equal(t, []string{"super-secret-repo"}, s.repos)
	assert.True(t, gock.IsDone())
}

func TestEnumerateWithApp(t *testing.T) {
	defer gock.Off()

	// generate a private key (it just needs to be in the right format)
	privateKey := func() string {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}

		data := x509.MarshalPKCS1PrivateKey(key)
		var pemKey bytes.Buffer
		pem.Encode(&pemKey, &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: data,
		})
		return pemKey.String()
	}()

	gock.New("https://api.github.com").
		Post("/app/installations/1337/access_tokens").
		Reply(200).
		JSON(map[string]string{"token": "dontlook"})

	gock.New("https://api.github.com").
		Get("/installation/repositories").
		Reply(200).
		JSON(map[string]string{})

	s := initTestSource(nil)
	_, _, err := s.enumerateWithApp(
		context.TODO(),
		"https://api.github.com",
		&credentialspb.GitHubApp{
			InstallationId: "1337",
			AppId:          "4141",
			PrivateKey:     privateKey,
		},
	)
	fmt.Println(err)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(s.repos))

	assert.True(t, gock.IsDone())
}

// func TestSource_paginateRepos(t *testing.T) {
// 	type args struct {
// 		ctx       context.Context
// 		apiClient *github.Client
// 	}
// 	tests := []struct {
// 		name string
// 		org  string
// 		args args
// 	}{
// 		{
// 			org: "fakeNetflix",
// 			args: args{
// 				ctx:       context.Background(),
// 				apiClient: github.NewClient(common.SaneHttpClient()),
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			s := &Source{httpClient: common.SaneHttpClient()}
// 			s.paginateRepos(tt.args.ctx, tt.args.apiClient, tt.org)
// 			if len(s.repos) < 101 {
// 				t.Errorf("expected > 100 repos, got %d", len(s.repos))
// 			}
// 		})
// 	}
// }
