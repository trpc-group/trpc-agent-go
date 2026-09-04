//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheURLRoot_RemoveAllError(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	zipBytes := buildZip(t, map[string]string{
		"alpha/" + skillFile: "---\nname: alpha\n" +
			"description: d\n---\nbody\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipBytes)
		},
	))
	defer srv.Close()

	urlRoot := localhostServerURL(t, srv.URL) + "/skills.zip"
	key := sha256Hex(urlRoot)
	destDir := filepath.Join(cacheDir, key)
	require.NoError(t, os.MkdirAll(destDir, dirPerm))
	require.NoError(t, os.WriteFile(
		filepath.Join(destDir, "x"), []byte("x"), filePerm,
	))
	require.NoError(t, os.Chmod(destDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(destDir, dirPerm) })
	if err := os.Remove(filepath.Join(destDir, "x")); err == nil {
		t.Skip("skip due to permission policy: expected remove to fail")
	}

	_, err := NewFSRepository(urlRoot)
	require.Error(t, err)
}

func TestCacheURLRoot_CacheDirIsFileFails(t *testing.T) {
	cacheFile := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(cacheFile, []byte("x"), filePerm))
	t.Setenv(EnvSkillsCacheDir, cacheFile)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("x"))
		},
	))
	defer srv.Close()

	_, err := NewFSRepository(
		localhostServerURL(t, srv.URL) + "/skills.zip",
	)
	require.Error(t, err)
}

func TestCacheURLRoot_CacheDirNoWriteFails(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, os.Chmod(cacheDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(cacheDir, dirPerm) })
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("x"))
		},
	))
	defer srv.Close()

	_, err := NewFSRepository(
		localhostServerURL(t, srv.URL) + "/skills.zip",
	)
	require.Error(t, err)
}

func TestDownloadURLToFile_Errors(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		u, err := url.Parse("http://localhost/skills.zip")
		require.NoError(t, err)
		err = downloadURLToFileWithTimeout(
			u,
			filepath.Join(t.TempDir(), "x"),
			0,
		)
		require.Error(t, err)
	})

	t.Run("http-get", func(t *testing.T) {
		u := &url.URL{Scheme: "http"}
		err := downloadURLToFile(u, filepath.Join(t.TempDir(), "x"))
		require.Error(t, err)
	})

	t.Run("request", func(t *testing.T) {
		u := &url.URL{Scheme: "http", Host: "example.com:bad"}
		err := downloadURLToFile(u, filepath.Join(t.TempDir(), "x"))
		require.Error(t, err)
	})

	t.Run("create", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("x"))
			},
		))
		defer srv.Close()

		u, err := url.Parse(localhostServerURL(t, srv.URL))
		require.NoError(t, err)
		require.Error(t, downloadURLToFile(u, ""))
	})

	t.Run("copy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "10")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("x"))
			},
		))
		defer srv.Close()

		u, err := url.Parse(localhostServerURL(t, srv.URL))
		require.NoError(t, err)
		err = downloadURLToFile(u, filepath.Join(t.TempDir(), "x"))
		require.Error(t, err)
	})
}

func TestDownloadURLToFile_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	))
	defer srv.Close()

	u, err := url.Parse(localhostServerURL(t, srv.URL))
	require.NoError(t, err)

	start := time.Now()
	err = downloadURLToFileWithTimeout(
		u,
		filepath.Join(t.TempDir(), "skills.zip"),
		50*time.Millisecond,
	)
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
}

func TestDownloadURLToFile_RedirectPolicy(t *testing.T) {
	t.Run("cross-origin-loopback", func(t *testing.T) {
		targetHit := make(chan struct{}, 1)
		target := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				targetHit <- struct{}{}
				_, _ = w.Write([]byte("skills"))
			},
		))
		defer target.Close()
		targetURL := localhostServerURL(t, target.URL)

		redirector := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(
					w,
					r,
					targetURL,
					http.StatusFound,
				)
			},
		))
		defer redirector.Close()

		u, err := url.Parse(localhostServerURL(t, redirector.URL))
		require.NoError(t, err)
		err = downloadURLToFileWithTimeout(
			u,
			filepath.Join(t.TempDir(), "skills.zip"),
			time.Second,
		)
		require.Error(t, err)

		select {
		case <-targetHit:
			t.Fatal("redirect target received a request")
		default:
		}
	})

	t.Run("same-origin-loopback", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/skills.zip", http.StatusFound)
		})
		mux.HandleFunc("/skills.zip", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("skills"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		u, err := url.Parse(
			localhostServerURL(t, srv.URL) + "/start",
		)
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "skills.zip")
		require.NoError(t, downloadURLToFileWithTimeout(
			u,
			path,
			time.Second,
		))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, []byte("skills"), got)
	})

	t.Run("too-many", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/loop", http.StatusFound)
			},
		))
		defer srv.Close()

		u, err := url.Parse(localhostServerURL(t, srv.URL) + "/loop")
		require.NoError(t, err)
		err = downloadURLToFileWithTimeout(
			u,
			filepath.Join(t.TempDir(), "skills.zip"),
			time.Second,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "stopped after 10 redirects")
	})
}

func TestDownloadURLToFile_RejectsPrivateAddress(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"169.254.169.254",
	} {
		t.Run(host, func(t *testing.T) {
			u, err := url.Parse("http://" + host + "/skills.zip")
			require.NoError(t, err)

			err = downloadURLToFileWithTimeout(
				u,
				filepath.Join(t.TempDir(), "skills.zip"),
				time.Second,
			)
			require.Error(t, err)
			require.Contains(t, err.Error(), "private or local")
		})
	}
}

func TestValidateSkillsRootURL(t *testing.T) {
	tests := []struct {
		name           string
		rawURL         string
		allowLocalhost bool
		wantErr        bool
	}{
		{name: "nil", wantErr: true},
		{name: "scheme", rawURL: "ftp://example.com", wantErr: true},
		{name: "host", rawURL: "http:///skills.zip", wantErr: true},
		{name: "localhost-blocked", rawURL: "http://localhost", wantErr: true},
		{
			name:           "localhost-allowed",
			rawURL:         "http://localhost",
			allowLocalhost: true,
		},
		{name: "loopback", rawURL: "http://[::1]", wantErr: true},
		{name: "private", rawURL: "http://[fc00::1]", wantErr: true},
		{name: "link-local", rawURL: "http://[fe80::1]", wantErr: true},
		{name: "multicast", rawURL: "http://[ff02::1]", wantErr: true},
		{name: "unspecified", rawURL: "http://[::]", wantErr: true},
		{name: "public", rawURL: "https://8.8.8.8"},
		{name: "hostname", rawURL: "https://skills.example.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u *url.URL
			if tt.rawURL != "" {
				var err error
				u, err = url.Parse(tt.rawURL)
				require.NoError(t, err)
			}
			err := validateSkillsRootURL(u, tt.allowLocalhost)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveSkillsRootDialAddresses(t *testing.T) {
	tests := []struct {
		name             string
		address          string
		allowedLocalhost string
		addresses        []net.IPAddr
		resolveErr       error
		want             []string
		wantErr          bool
	}{
		{
			name:    "invalid-address",
			address: "skills.example.test",
			wantErr: true,
		},
		{name: "empty-host", address: ":443", wantErr: true},
		{
			name:       "resolve-error",
			address:    "skills.example.test:443",
			resolveErr: errors.New("lookup failed"),
			wantErr:    true,
		},
		{
			name:    "no-addresses",
			address: "skills.example.test:443",
			wantErr: true,
		},
		{
			name:    "private-address",
			address: "skills.example.test:443",
			addresses: []net.IPAddr{
				{IP: net.ParseIP("192.168.1.10")},
			},
			wantErr: true,
		},
		{
			name:    "public-addresses",
			address: "skills.example.test:443",
			addresses: []net.IPAddr{
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("2001:4860:4860::8888")},
			},
			want: []string{
				"8.8.8.8:443",
				"[2001:4860:4860::8888]:443",
			},
		},
		{
			name:             "localhost-allowed",
			address:          "localhost:8080",
			allowedLocalhost: "localhost:8080",
			addresses: []net.IPAddr{
				{IP: net.ParseIP("127.0.0.1")},
			},
			want: []string{"127.0.0.1:8080"},
		},
		{
			name:             "localhost-wrong-port",
			address:          "localhost:8081",
			allowedLocalhost: "localhost:8080",
			wantErr:          true,
		},
		{
			name:             "localhost-must-remain-loopback",
			address:          "localhost:8080",
			allowedLocalhost: "localhost:8080",
			addresses: []net.IPAddr{
				{IP: net.ParseIP("8.8.8.8")},
			},
			wantErr: true,
		},
		{
			name:    "public-ip-literal",
			address: "8.8.4.4:80",
			want:    []string{"8.8.4.4:80"},
		},
		{
			name:    "ipv6-zone",
			address: "skills.example.test:443",
			addresses: []net.IPAddr{
				{
					IP:   net.ParseIP("2001:4860:4860::8888"),
					Zone: "eth0",
				},
			},
			want: []string{"[2001:4860:4860::8888%eth0]:443"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &stubSkillsRootIPResolver{
				addresses: tt.addresses,
				err:       tt.resolveErr,
			}
			got, err := resolveSkillsRootDialAddresses(
				context.Background(),
				tt.address,
				tt.allowedLocalhost,
				resolver,
			)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSkillsRootHTTPClient_DialsValidatedIP(t *testing.T) {
	u, err := url.Parse("http://skills.example.test/archive.zip")
	require.NoError(t, err)

	t.Run("public", func(t *testing.T) {
		resolver := &stubSkillsRootIPResolver{
			addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}},
		}
		dialErr := errors.New("stop after vetted dial")
		dialer := &stubSkillsRootNetworkDialer{err: dialErr}
		client := newSkillsRootHTTPClient(u, time.Second, resolver, dialer)
		defer client.CloseIdleConnections()

		_, err := client.Get(u.String())
		require.ErrorIs(t, err, dialErr)
		require.Equal(t, 1, resolver.calls)
		require.Equal(t, []string{"8.8.8.8:80"}, dialer.addresses)
		transport, ok := client.Transport.(*http.Transport)
		require.True(t, ok)
		require.Nil(t, transport.Proxy)
	})

	t.Run("rebound-private", func(t *testing.T) {
		resolver := &stubSkillsRootIPResolver{
			addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
		}
		dialer := &stubSkillsRootNetworkDialer{
			err: errors.New("must not dial"),
		}
		client := newSkillsRootHTTPClient(u, time.Second, resolver, dialer)
		defer client.CloseIdleConnections()

		_, err := client.Get(u.String())
		require.Error(t, err)
		require.Contains(t, err.Error(), "private or local")
		require.Equal(t, 1, resolver.calls)
		require.Empty(t, dialer.addresses)
	})
}

func TestSameURLOrigin(t *testing.T) {
	httpsURL, err := url.Parse("https://example.com/skills.zip")
	require.NoError(t, err)
	explicitHTTPSURL, err := url.Parse(
		"https://EXAMPLE.com:443/redirected.zip",
	)
	require.NoError(t, err)
	otherPortURL, err := url.Parse("https://example.com:8443/skills.zip")
	require.NoError(t, err)
	httpURL, err := url.Parse("http://example.com/skills.zip")
	require.NoError(t, err)
	explicitHTTPURL, err := url.Parse("http://example.com:80/skills.zip")
	require.NoError(t, err)

	require.True(t, sameURLOrigin(httpsURL, explicitHTTPSURL))
	require.True(t, sameURLOrigin(httpURL, explicitHTTPURL))
	require.False(t, sameURLOrigin(httpsURL, otherPortURL))
	require.False(t, sameURLOrigin(nil, httpsURL))
}

type stubSkillsRootIPResolver struct {
	addresses []net.IPAddr
	err       error
	calls     int
}

func (r *stubSkillsRootIPResolver) LookupIPAddr(
	context.Context,
	string,
) ([]net.IPAddr, error) {
	r.calls++
	return r.addresses, r.err
}

type stubSkillsRootNetworkDialer struct {
	addresses []string
	err       error
}

func (d *stubSkillsRootNetworkDialer) DialContext(
	_ context.Context,
	_ string,
	address string,
) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, d.err
}

func localhostServerURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	u.Host = "localhost:" + u.Port()
	return u.String()
}

func TestExtractZipFile_Errors(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var total int64
		require.Error(t, extractZipFile(nil, t.TempDir(), &total))
	})

	t.Run("clean-empty", func(t *testing.T) {
		var total int64
		require.NoError(t, extractZipFile(&zip.File{
			FileHeader: zip.FileHeader{Name: "."},
		}, t.TempDir(), &total))
	})

	t.Run("mkdirall", func(t *testing.T) {
		destFile := filepath.Join(t.TempDir(), "dest")
		require.NoError(t, os.WriteFile(destFile, []byte("x"), filePerm))

		f := &zip.File{
			FileHeader: zip.FileHeader{Name: "alpha/" + skillFile},
		}
		require.Error(t, extractZipFile(f, destFile, nil))
	})

	t.Run("openfile", func(t *testing.T) {
		destDir := t.TempDir()
		target := filepath.Join(destDir, "alpha", skillFile)
		require.NoError(t, os.MkdirAll(target, dirPerm))

		zipBytes := buildZip(t, map[string]string{
			"alpha/" + skillFile: "x",
		})
		src := filepath.Join(t.TempDir(), "skills.zip")
		require.NoError(t, os.WriteFile(src, zipBytes, filePerm))

		zr, err := zip.OpenReader(src)
		require.NoError(t, err)
		defer zr.Close()

		require.NotEmpty(t, zr.File)
		require.Error(t, extractZipFile(zr.File[0], destDir, nil))
	})

	t.Run("copy", func(t *testing.T) {
		data := buildStoredZip(t, "alpha/"+skillFile, []byte("hi"))
		idx := bytes.Index(data, []byte("hi"))
		require.GreaterOrEqual(t, idx, 0)
		data[idx] ^= 0xff

		src := filepath.Join(t.TempDir(), "skills.zip")
		require.NoError(t, os.WriteFile(src, data, filePerm))

		zr, err := zip.OpenReader(src)
		require.NoError(t, err)
		defer zr.Close()

		destDir := t.TempDir()
		require.NotEmpty(t, zr.File)
		require.Error(t, extractZipFile(zr.File[0], destDir, nil))
	})

	t.Run("total", func(t *testing.T) {
		zipBytes := buildZip(t, map[string]string{
			"alpha/" + skillFile: "x",
		})
		src := filepath.Join(t.TempDir(), "skills.zip")
		require.NoError(t, os.WriteFile(src, zipBytes, filePerm))

		zr, err := zip.OpenReader(src)
		require.NoError(t, err)
		defer zr.Close()

		destDir := t.TempDir()
		total := int64(maxExtractTotalBytes)
		require.NotEmpty(t, zr.File)
		require.Error(t, extractZipFile(zr.File[0], destDir, &total))
	})
}

func TestExtractTarReader_Errors(t *testing.T) {
	t.Run("path", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: "../x",
			Mode: filePerm,
			Size: int64(len("x")),
		}))
		_, _ = tw.Write([]byte("x"))
		_ = tw.Close()

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			t.TempDir(),
		)
		require.Error(t, err)
	})

	t.Run("clean-empty", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     ".",
			Typeflag: tar.TypeDir,
			Mode:     dirPerm,
		}))
		require.NoError(t, tw.Close())

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			t.TempDir(),
		)
		require.NoError(t, err)
	})

	t.Run("mkdirall-dir", func(t *testing.T) {
		destFile := filepath.Join(t.TempDir(), "dest")
		require.NoError(t, os.WriteFile(destFile, []byte("x"), filePerm))

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "alpha",
			Typeflag: tar.TypeDir,
			Mode:     dirPerm,
		}))
		require.NoError(t, tw.Close())

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			destFile,
		)
		require.Error(t, err)
	})

	t.Run("mkdirall-file-parent", func(t *testing.T) {
		destFile := filepath.Join(t.TempDir(), "dest")
		require.NoError(t, os.WriteFile(destFile, []byte("x"), filePerm))

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "alpha/" + skillFile,
			Typeflag: tar.TypeReg,
			Mode:     filePerm,
			Size:     1,
		}))
		_, _ = tw.Write([]byte("x"))
		require.NoError(t, tw.Close())

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			destFile,
		)
		require.Error(t, err)
	})

	t.Run("size", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "big.txt",
			Typeflag: tar.TypeReg,
			Mode:     filePerm,
			Size:     maxExtractFileBytes + 1,
		}))
		_ = tw.Close()

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			t.TempDir(),
		)
		require.Error(t, err)
	})

	t.Run("openfile", func(t *testing.T) {
		destDir := t.TempDir()
		target := filepath.Join(destDir, "alpha", skillFile)
		require.NoError(t, os.MkdirAll(target, dirPerm))

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "alpha/" + skillFile,
			Typeflag: tar.TypeReg,
			Mode:     filePerm,
			Size:     1,
		}))
		_, _ = tw.Write([]byte("x"))
		require.NoError(t, tw.Close())

		err := extractTarReader(
			tar.NewReader(bytes.NewReader(buf.Bytes())),
			destDir,
		)
		require.Error(t, err)
	})
}

func TestWriteSingleSkillFile_Errors(t *testing.T) {
	err := writeSingleSkillFile(filepath.Join(t.TempDir(), "nope"),
		t.TempDir())
	require.Error(t, err)

	tmp := t.TempDir()
	src := filepath.Join(tmp, skillFile)
	require.NoError(t, os.WriteFile(src, []byte("x"), filePerm))
	require.NoError(t, os.Truncate(src, maxExtractFileBytes+1))
	require.Error(t, writeSingleSkillFile(src, t.TempDir()))
}

func buildStoredZip(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	w, err := zw.CreateHeader(hdr)
	require.NoError(t, err)
	_, err = w.Write(body)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}
