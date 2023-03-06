package git

import (
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/BurntSushi/toml"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/pkg/errors"
)

type Fetcher struct {
	Logger   *log.Logger
	Keychain GitKeychain
}

func (f Fetcher) Fetch(dir, gitURL, gitRevision, metadataDir string) error {
	f.Logger.Printf("Cloning %q @ %q...", gitURL, gitRevision)
	auth, err := f.Keychain.Resolve(gitURL)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "git-clone-")
	if err != nil {
		return err
	}

	repository, err := gogit.PlainInit(tmpDir, false)
	if err != nil {
		return errors.Wrap(err, "initializing repo")
	}

	remote, err := repository.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{gitURL},
	})
	if err != nil {
		return errors.Wrap(err, "creating remote")
	}

	httpsTransport, err := getHttpsTransport()
	if err != nil {
		return err
	}
	client.InstallProtocol("https", httpsTransport)

	err = remote.Fetch(&gogit.FetchOptions{
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Auth:     auth,
	})
	if err != nil && err != transport.ErrAuthenticationRequired {
		return errors.Wrapf(err, "unable to fetch references for repository")
	} else if err == transport.ErrAuthenticationRequired {
		return errors.Wrapf(err, "invalid credentials for repository")
	}

	worktree, err := repository.Worktree()
	if err != nil {
		return errors.Wrapf(err, "getting worktree for repository")
	}

	hash, err := repository.ResolveRevision(plumbing.Revision(gitRevision))
	if err != nil {
		return errors.Wrapf(err, "resolving revision")
	}

	err = worktree.Checkout(&gogit.CheckoutOptions{Hash: *hash})
	if err != nil {
		return errors.Wrapf(err, "checking out revision")
	}

	// copy tmpDir to dir
	err = os.RemoveAll(dir)
	if err != nil {
		return errors.Wrapf(err, "removing directory")
	}

	// recursive copy of tmpDir to dir
	err = copyDir(dir, tmpDir)
	if err != nil {
		return errors.Wrapf(err, "copying directory")
	}

	// Write the git revision to the metadata directory
	projectMetadataFile, err := os.Create(path.Join(metadataDir, "project-metadata.toml"))
	if err != nil {
		return errors.Wrapf(err, "invalid metadata destination '%s/project-metadata.toml' for git repository: %s", metadataDir, gitURL)
	}
	defer projectMetadataFile.Close()

	projectMd := project{
		Source: source{
			Type: "git",
			Metadata: metadata{
				Repository: gitURL,
				Revision:   gitRevision,
			},
			Version: version{
				Commit: hash.String(),
			},
		},
	}
	if err := toml.NewEncoder(projectMetadataFile).Encode(projectMd); err != nil {
		return errors.Wrapf(err, "invalid metadata destination '%s/project-metadata.toml' for git repository: %s", metadataDir, gitRevision)
	}

	f.Logger.Printf("Successfully cloned %q @ %q in path %q", gitURL, gitRevision, dir)
	return nil
}

func getHttpsTransport() (transport.Transport, error) {
	if httpsProxy, exists := os.LookupEnv("HTTPS_PROXY"); exists {
		parsedUrl, err := url.Parse(httpsProxy)
		if err != nil {
			return nil, errors.Wrap(err, "parsing HTTPS_PROXY url")
		}
		proxyClient := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(parsedUrl),
			},
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		return githttp.NewClient(proxyClient), nil
	} else {
		return githttp.DefaultClient, nil
	}
}

func copyFile(dstPath string, srcPath string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	return os.Chmod(dstPath, srcInfo.Mode())
}

// recursively copy the source directory to the destination directory
func copyDir(dstDir string, srcDir string) error {
	srcInfo, err := os.Stat(srcDir)
	if err != nil {
		return err
	}

	err = os.MkdirAll(dstDir, srcInfo.Mode())
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	fileInfos, err := getFileInfos(entries)
	if err != nil {
		return err
	}

	for _, fileInfo := range fileInfos {
		srcPath := path.Join(srcDir, fileInfo.Name())
		dstPath := path.Join(dstDir, fileInfo.Name())

		if fileInfo.IsDir() {
			if err = copyDir(dstPath, srcPath); err != nil {
				return err
			}
		} else {
			if err = copyFile(dstPath, srcPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func getFileInfos(entries []os.DirEntry) ([]fs.FileInfo, error) {
	fileInfos := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		fileInfos = append(fileInfos, info)
	}
	return fileInfos, nil
}

type project struct {
	Source source `toml:"source"`
}

type source struct {
	Type     string   `toml:"type"`
	Metadata metadata `toml:"metadata"`
	Version  version  `toml:"version"`
}

type metadata struct {
	Repository string `toml:"repository"`
	Revision   string `toml:"revision"`
}

type version struct {
	Commit string `toml:"commit"`
}
