package jupyter

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestWithIP(t *testing.T) {
	type args struct {
		ip string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "normal IPv4 address",
			args: args{ip: "192.168.1.1"},
			want: "192.168.1.1",
		},
		{
			name: "IPv6 address",
			args: args{ip: "2001:db8::1"},
			want: "2001:db8::1",
		},
		{
			name: "empty string",
			args: args{ip: ""},
			want: "",
		},
		{
			name: "localhost",
			args: args{ip: "localhost"},
			want: "localhost",
		},
		{
			name: "special characters",
			args: args{ip: "0.0.0.0"},
			want: "0.0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			ce := &CodeExecutor{
				ip: "default",
			}

			opt := WithIP(tt.args.ip)

			opt(ce)

			assert.Equal(t, tt.want, ce.ip, "WithIP() set unexpected ip value")
		})
	}
}

func TestWithToken(t *testing.T) {
	type args struct {
		token string
	}
	tests := []struct {
		name      string
		args      args
		wantToken string
	}{
		{
			name:      "normal token",
			args:      args{token: "valid-token-123"},
			wantToken: "valid-token-123",
		},
		{
			name:      "empty token",
			args:      args{token: ""},
			wantToken: "",
		},
		{
			name:      "special characters token",
			args:      args{token: "!@#$%^&*()_+-=[]{}|;:'\",.<>?/"},
			wantToken: "!@#$%^&*()_+-=[]{}|;:'\",.<>?/",
		},
		{
			name:      "long token",
			args:      args{token: strings.Repeat("a", 1024)},
			wantToken: strings.Repeat("a", 1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			c := &CodeExecutor{}

			opt := WithToken(tt.args.token)
			opt(c)

			assert.Equal(t, tt.wantToken, c.token, "token should match expected value")
		})
	}
}

func TestWithStartTimeout(t *testing.T) {
	type args struct {
		timeout time.Duration
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "normal positive timeout",
			args: args{timeout: 5 * time.Second},
			want: 5 * time.Second,
		},
		{
			name: "zero timeout boundary",
			args: args{timeout: 0},
			want: 0,
		},
		{
			name: "negative timeout edge case",
			args: args{timeout: -1 * time.Second},
			want: -1 * time.Second,
		},
		{
			name: "maximum duration value",
			args: args{timeout: time.Duration(1<<63 - 1)},
			want: time.Duration(1<<63 - 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			ce := &CodeExecutor{}

			option := WithStartTimeout(tt.args.timeout)
			option(ce)

			if ce.startTimeout != tt.want {
				t.Errorf("startTimeout = %v, want %v", ce.startTimeout, tt.want)
			}
		})
	}
}

func TestWithKernelName(t *testing.T) {
	type args struct {
		kernelName string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "normal kernel name",
			args: args{kernelName: "python3"},
			want: "python3",
		},
		{
			name: "empty kernel name",
			args: args{kernelName: ""},
			want: "",
		},
		{
			name: "special characters",
			args: args{kernelName: "my-kernel@123"},
			want: "my-kernel@123",
		},
		{
			name: "long kernel name",
			args: args{kernelName: "a-very-long-kernel-name-with-many-characters-1234567890"},
			want: "a-very-long-kernel-name-with-many-characters-1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opt := WithKernelName(tt.args.kernelName)

			ce := &CodeExecutor{}

			opt(ce)

			if ce.kernelName != tt.want {
				t.Errorf(
					"WithKernelName() set kernelName to %v, want %v",
					ce.kernelName,
					tt.want,
				)
			}
		})
	}
}

func TestWithPort(t *testing.T) {
	type args struct {
		port int
	}
	tests := []struct {
		name     string
		args     args
		wantPort int
	}{
		{
			name:     "normal positive port",
			args:     args{port: 8888},
			wantPort: 8888,
		},
		{
			name:     "zero port",
			args:     args{port: 0},
			wantPort: 0,
		},
		{
			name:     "negative port",
			args:     args{port: -1},
			wantPort: -1,
		},
		{
			name:     "max valid port",
			args:     args{port: 65535},
			wantPort: 65535,
		},
		{
			name:     "port exceeding uint16",
			args:     args{port: 65536},
			wantPort: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			opt := WithPort(tt.args.port)

			ce := &CodeExecutor{}

			opt(ce)

			if ce.port != tt.wantPort {
				t.Errorf(
					"WithPort() applied port = %v, want %v",
					ce.port,
					tt.wantPort,
				)
			}
		})
	}
}

func Test_silencePip(t *testing.T) {
	type args struct {
		code string
		lang string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "python code with !pip install",
			args: args{
				code: "! pip install requests",
				lang: "python",
			},
			want: "! pip install -qqq requests",
		},
		{
			name: "bash code with pip install",
			args: args{
				code: "pip install numpy",
				lang: "bash",
			},
			want: "pip install -qqq numpy",
		},
		{
			name: "multiple lines in python",
			args: args{
				code: "! pip install a\n! pip install b",
				lang: "python",
			},
			want: "! pip install -qqq a\n! pip install -qqq b",
		},
		{
			name: "already has -qqq",
			args: args{
				code: "! pip install -qqq pandas",
				lang: "python",
			},
			want: "! pip install -qqq pandas",
		},
		{
			name: "other language (java)",
			args: args{
				code: "pip install something",
				lang: "java",
			},
			want: "pip install something",
		},
		{
			name: "empty code",
			args: args{
				code: "",
				lang: "python",
			},
			want: "",
		},
		{
			name: "mixed lines in python",
			args: args{
				code: "! pip install a\nprint('hello')\npip install b",
				lang: "python",
			},
			want: "! pip install -qqq a\nprint('hello')\npip install b",
		},
		{
			name: "shell language",
			args: args{
				code: "pip install flask",
				lang: "shell",
			},
			want: "pip install -qqq flask",
		},
		{
			name: "powershell language",
			args: args{
				code: "pip install django",
				lang: "powershell",
			},
			want: "pip install -qqq django",
		},
		{
			name: "multiple lines with some -qqq",
			args: args{
				code: "! pip install a\npip install -qqq b",
				lang: "python",
			},
			want: "! pip install -qqq a\npip install -qqq b",
		},
		{
			name: "python code without !",
			args: args{
				code: "pip install c",
				lang: "python",
			},
			want: "pip install c",
		},
		{
			name: "bash with multiple spaces",
			args: args{
				code: "pip   install flask",
				lang: "bash",
			},
			want: "pip   install flask",
		},
		{
			name: "powershell with comment",
			args: args{
				code: "pip install django # quiet",
				lang: "powershell",
			},
			want: "pip install -qqq django # quiet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := silencePip(tt.args.code, tt.args.lang); got != tt.want {
				t.Errorf("silencePip() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithLogFile(t *testing.T) {
	c := &CodeExecutor{}
	WithLogFile("test.log")(c)
	assert.Equal(t, "test.log", c.logFile)
}

func TestWithLogLevel(t *testing.T) {
	c := &CodeExecutor{}
	WithLogLevel("debug")(c)
	assert.Equal(t, "debug", c.logLevel)
}

func TestWithWaitReadyTimeout(t *testing.T) {
	c := &CodeExecutor{}
	WithWaitReadyTimeout(10 * time.Second)(c)
	assert.Equal(t, 10*time.Second, c.waitReadyTimeout)
}

func isJupyterInstalled() bool {
	cmd := exec.Command("python", "-m", "jupyter", "kernelgateway", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func TestExecuteCode(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	codeExecutor := &CodeExecutor{
		cli: srv.cli,
	}

	code := "print('hello world')"
	result, err := codeExecutor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: code, Language: "python"},
		},
		ExecutionID: "test",
	})
	if err != nil {
		t.Fatalf("ExecuteCode failed: %v", err)
	}
	assert.Equal(t, "", result.Output)
}

func Test_cleanup(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	_, cancel := context.WithCancel(context.Background())
	codeExecutor := &CodeExecutor{
		cli:    srv.cli,
		cancel: cancel,
	}

	codeExecutor.cleanup()
}

func Test_generateToken(t *testing.T) {
	token := generateToken()
	assert.NotEmpty(t, token)
}

func Test_checkJupyterGateway(t *testing.T) {
	codeExecutor := &CodeExecutor{}
	err := codeExecutor.checkJupyterGateway()
	if !isJupyterInstalled() {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
	}
}

func TestCodeBlockDelimiter(t *testing.T) {
	codeExecutor := &CodeExecutor{}
	delimiter := codeExecutor.CodeBlockDelimiter()
	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)

	cli := &Client{}
	delimiter = cli.CodeBlockDelimiter()
	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestNew(t *testing.T) {
	if !isJupyterInstalled() {
		_, err := New(WithLogFile("/tmp/jupyter.log"), WithPort(9999))
		assert.Error(t, err)
		return
	}
	codeExecutor, err := New()
	defer codeExecutor.Close()
	assert.NoError(t, err)
}
