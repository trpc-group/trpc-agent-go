package safety

import (
	"strings"
)

// ---------- Rule 1: 危险命令检测 ----------

// DangerousCommandRule checks for explicitly dangerous commands.
//
// It detects:
//   - Destructive filesystem operations (rm -rf, format, dd)
//   - Reading sensitive files (~/.ssh, /etc/passwd, .env)
//   - Commands that leak credentials (cat ~/.aws/credentials)
type DangerousCommandRule struct {
	// 危险命令关键词列表
	dangerousCommands []string
	// 敏感路径关键词列表
	sensitivePaths []string
}

// NewDangerousCommandRule creates a rule with the default deny list.
func NewDangerousCommandRule() *DangerousCommandRule {
	return &DangerousCommandRule{
		dangerousCommands: []string{
			// === 文件系统破坏 ===
			"rm -rf /",  // 删除根目录
			"rm -rf ~",  // 删除用户目录
			"rm -rf .",  // 删除当前目录
			"rm -rf /*", // 删除根下所有文件
			"dd if=",    // 磁盘写入（可能覆盖分区）
			"mkfs.",     // 格式化文件系统
			"fdisk",     // 磁盘分区操作
			"shutdown",  // 关机
			"reboot",    // 重启
			"halt",      // 停机
			"poweroff",  // 断电
			"init 0",    // 切换运行级别到关机
			"init 6",    // 切换运行级别到重启
			// === 权限提升 / 提权 ===
			"chmod 777 /", // 开放根目录权限
			"chown -R",    // 递归修改所有者（可能破坏系统）
			"setfacl -R",  // 递归修改 ACL
		},
		sensitivePaths: []string{
			// === 凭据文件 ===
			".ssh/id_rsa",      // SSH 私钥
			".ssh/authorized",  // SSH 授权密钥
			".aws/credentials", // AWS 凭据
			".gcloud/",         // GCP 凭据
			"credentials.json", // 通用凭据文件
			// === 密码和配置 ===
			".env",        // 环境变量（通常含密钥）
			".env.",       // .env.production 等变体
			"/etc/shadow", // Linux 密码哈希
			"/etc/passwd", // 用户账户信息
			// === 证书和密钥 ===
			".pem",       // 证书私钥
			".key",       // 私钥文件
			".p12",       // PKCS#12 密钥库
			".pfx",       // PKCS#12 密钥库
			"id_ed25519", // Ed25519 SSH 密钥
			// === 数据库和日志（可能含敏感信息）===
			".sql",   // 数据库导出
			".dump",  // 数据库转储
			"backup", // 备份文件
		},
	}
}

func (r *DangerousCommandRule) ID() string {
	return "danger_cmd_001"
}

func (r *DangerousCommandRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}

	// 1. 检查危险命令关键词
	for _, keyword := range r.dangerousCommands {
		if strings.Contains(cmd, keyword) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  keyword,
				Reason:    "命令包含危险操作：" + keyword + "。该命令可能导致系统损坏或数据丢失。",
			}
		}
	}

	// 2. 检查敏感路径读取
	for _, path := range r.sensitivePaths {
		if strings.Contains(cmd, path) {
			// 区分"读取"和"写入"
			if strings.Contains(cmd, "cat ") || strings.Contains(cmd, "less ") ||
				strings.Contains(cmd, "head ") || strings.Contains(cmd, "tail ") ||
				strings.Contains(cmd, "grep ") || strings.Contains(cmd, "cp ") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  path,
					Reason:    "尝试读取敏感文件：" + path + "。该文件可能包含凭据或隐私信息。",
				}
			}
			// 写入/删除敏感路径
			if strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") ||
				strings.Contains(cmd, ">") {
				return &ScanResult{
					Decision:  DecisionDeny,
					RiskLevel: RiskCritical,
					RuleID:    r.ID(),
					Evidence:  path,
					Reason:    "尝试修改/删除敏感文件：" + path + "。",
				}
			}
			// 其他访问（列出目录等）
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  path,
				Reason:    "尝试访问敏感路径：" + path + "。",
			}
		}
	}

	return nil // 未触发规则
}

// ---------- Rule 2: 网络外连检测 ----------

// NetworkAccessRule detects commands that connect to external networks.
type NetworkAccessRule struct {
	dangerousCmds []string
}

func NewNetworkAccessRule() *NetworkAccessRule {
	return &NetworkAccessRule{
		dangerousCmds: []string{
			"curl",        // HTTP 客户端
			"wget",        // 文件下载
			"nc ",         // netcat（注意空格避免误匹配 ncdu 等）
			"ncat",        // nmap netcat
			"telnet",      // 远程登录
			"ssh ",        // SSH 远程连接
			"scp ",        // 远程文件复制
			"sftp",        // SSH 文件传输
			"rsync",       // 远程同步
			"nslookup",    // DNS 查询
			"dig ",        // DNS 查询
			"host ",       // DNS 查询
			"nmap",        // 端口扫描
			"socat",       // 网络中继
			"git clone",   // Git 克隆外部仓库
			"pip install", // Python 安装外部包
			"npm install", // Node.js 安装外部包
		},
	}
}

func (r *NetworkAccessRule) ID() string {
	return "network_002"
}

func (r *NetworkAccessRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}

	for _, keyword := range r.dangerousCmds {
		if strings.Contains(cmd, keyword) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  keyword,
				Reason:    "命令尝试进行网络访问：" + keyword + "。可能将数据外传或下载恶意内容。",
			}
		}
	}
	return nil
}

// ---------- Rule 3: Shell 绕过检测 ----------

// ShellBypassRule detects attempts to bypass shell safety restrictions
// by using -c flags, eval, or other indirect execution methods.
//
// Note: shellsafe already rejects $(), backticks, and redirections at
// the structural level. This rule catches semantic-level bypasses.
type ShellBypassRule struct {
	bypassPatterns []string
}

func NewShellBypassRule() *ShellBypassRule {
	return &ShellBypassRule{
		bypassPatterns: []string{
			"sh -c",      // sh 执行任意命令
			"bash -c",    // bash 执行任意命令
			"zsh -c",     // zsh 执行任意命令
			"python -c",  // Python 执行任意代码
			"python3 -c", // Python3 执行任意代码
			"perl -e",    // Perl 执行任意代码
			"ruby -e",    // Ruby 执行任意代码
			"node -e",    // Node.js 执行任意代码
			"eval ",      // Shell eval
			"exec ",      // Shell exec
			"source ",    // Shell source
			"xargs ",     // xargs 执行命令
			"env ",       // env 命令包装
			"sudo ",      // 提权执行
			"su ",        // 切换用户
			"base64 -d",  // 解码后执行
			"xxd -r",     // 十六进制解码
			"/dev/tcp/",  // Bash TCP 重定向
			"/dev/udp/",  // Bash UDP 重定向
		},
	}
}

func (r *ShellBypassRule) ID() string {
	return "shell_bypass_003"
}

func (r *ShellBypassRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}

	for _, pattern := range r.bypassPatterns {
		if strings.Contains(cmd, pattern) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  pattern,
				Reason:    "命令尝试绕过 shell 安全检查：" + pattern + "。可能通过间接执行方式运行任意命令。",
			}
		}
	}
	return nil
}

// ---------- Rule 4: 依赖安装与系统变更检测 ----------

type InstallAndMutateRule struct {
	patterns []string
}

func NewInstallAndMutateRule() *InstallAndMutateRule {
	return &InstallAndMutateRule{
		patterns: []string{
			// 包管理器安装
			"apt install", "apt-get install", "apt-get update",
			"yum install", "dnf install",
			"pacman -S", "brew install",
			"npm install", "npm i ",
			"pip install", "pip3 install",
			"go install", "go get ",
			"gem install", "cargo install",
			"snap install", "flatpak install",
			// 系统配置变更
			"systemctl enable", "systemctl start",
			"service start", "service enable",
			"update-rc.d",
			"crontab ",
			"iptables ", "nft ",
		},
	}
}

func (r *InstallAndMutateRule) ID() string { return "install_004" }

func (r *InstallAndMutateRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "命令尝试安装软件或修改系统配置：" + p + "。可能引入恶意依赖或破坏系统稳定性。",
			}
		}
	}
	return nil
}

// ---------- Rule 5: 宿主机执行风险检测 ----------

type HostExecRiskRule struct {
	risks []string
}

func NewHostExecRiskRule() *HostExecRiskRule {
	return &HostExecRiskRule{
		risks: []string{
			// PTY / 长会话风险
			"tty", "pty",
			// 后台进程
			"nohup ", "disown", "bg ", "fg ",
			// 进程残留 / 守护进程
			"daemon", "fork",
			// 提权 / 用户切换
			"sudo ", "su -", "su root",
			"chmod 777", "chmod -R 777",
			"chown root", "chown :root",
			"setuid", "setgid",
			// 内核模块 / 设备
			"insmod ", "modprobe ", "rmmod ",
			// 特殊文件系统
			"mount ", "umount ",
		},
	}
}

func (r *HostExecRiskRule) ID() string { return "hostexec_005" }

func (r *HostExecRiskRule) Check(input ScanInput) *ScanResult {
	// 只对 local executor 触发，container 中可以放宽
	if input.ExecutorType != "local" && input.ExecutorType != "" {
		return nil
	}
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}
	for _, risk := range r.risks {
		if strings.Contains(cmd, risk) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  risk,
				Reason:    "命令包含宿主机执行风险操作：" + risk + "。可能影响宿主机安全或稳定性。",
			}
		}
	}
	return nil
}

// ---------- Rule 6: 资源滥用检测 ----------

type ResourceAbuseRule struct{}

func NewResourceAbuseRule() *ResourceAbuseRule {
	return &ResourceAbuseRule{}
}

func (r *ResourceAbuseRule) ID() string { return "resource_006" }

func (r *ResourceAbuseRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}

	// 无限循环 / 长时间执行
	loopPatterns := []string{
		"while true", "while :", "for (( ; ; ))",
		"while [ 1 ]", "while [[ 1 ]]",
	}
	for _, lp := range loopPatterns {
		if strings.Contains(cmd, lp) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  lp,
				Reason:    "命令包含无限循环：" + lp + "。可能导致 CPU 或内存资源耗尽。",
			}
		}
	}

	// fork bomb 模式
	fbPatterns := []string{
		":(){ :|:& };:",  // 经典 fork bomb
		"() {",           // 函数定义（可能用于 fork bomb）
	}
	for _, fp := range fbPatterns {
		if strings.Contains(cmd, fp) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  fp,
				Reason:    "命令包含 fork bomb 模式，可能导致系统崩溃。",
			}
		}
	}

	// 资源消耗命令
	resourceCmds := []string{
		"stress ", "stress-ng",
		"yes ", "dd if=/dev/zero of=",
		">/dev/null", ": >",
		"sha256sum /dev/zero", "md5sum /dev/zero",
	}
	for _, rc := range resourceCmds {
		if strings.Contains(cmd, rc) {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskHigh,
				RuleID:    r.ID(),
				Evidence:  rc,
				Reason:    "命令尝试消耗大量系统资源：" + rc + "。",
			}
		}
	}
	return nil
}

// ---------- Rule 7: 敏感信息泄漏检测 ----------

type SensitiveInfoLeakRule struct {
	patterns []string
}

func NewSensitiveInfoLeakRule() *SensitiveInfoLeakRule {
	return &SensitiveInfoLeakRule{
		patterns: []string{
			// API Key / Token 模式
			"api_key", "apikey", "api_secret", "apisecret",
			"access_key", "secret_key",
			"private_key", "privatekey",
			// 密码字段
			"password", "passwd", "passphrase",
			"db_password", "db_pass",
			// Token
			"token", "bearer", "jwt",
			"auth_token", "refresh_token",
			// 输出重定向（可能把敏感数据写入文件）
			" > ", ">>",
		},
	}
}

func (r *SensitiveInfoLeakRule) ID() string { return "leak_007" }

func (r *SensitiveInfoLeakRule) Check(input ScanInput) *ScanResult {
	// 检查命令中是否在读取/打印敏感信息
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}

	// 检查 code blocks 中是否包含敏感信息关键词
	allText := cmd
	for _, cb := range input.CodeBlocks {
		allText += " " + strings.ToLower(cb.Code)
	}

	for _, p := range r.patterns {
		if !strings.Contains(allText, p) {
			continue
		}
		// 严重：echo/cat 输出 + 敏感词 + 重定向 = 泄漏
		if (strings.Contains(allText, "echo ") || strings.Contains(allText, "cat ") ||
			strings.Contains(allText, "printf ")) && strings.Contains(allText, ">") {
			return &ScanResult{
				Decision:  DecisionDeny,
				RiskLevel: RiskCritical,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "命令可能将敏感信息写入文件：" + p + "。存在凭据泄漏风险。",
			}
		}
	}
	return nil
}

// ---------- Rule 8: 人工复核（ask 决策）----------

// AskForReviewRule returns DecisionAsk for commands that are
// potentially risky but may have legitimate use cases.
type AskForReviewRule struct {
	patterns []string
}

func NewAskForReviewRule() *AskForReviewRule {
	return &AskForReviewRule{
		patterns: []string{
			"rm -r", "git push", "docker push",
			"kubectl delete", "drop table", "truncate ",
			"force",
		},
	}
}

func (r *AskForReviewRule) ID() string { return "ask_review_008" }

func (r *AskForReviewRule) Check(input ScanInput) *ScanResult {
	cmd := strings.ToLower(input.Command)
	if cmd == "" {
		return nil
	}
	for _, p := range r.patterns {
		if strings.Contains(cmd, p) {
			return &ScanResult{
				Decision:  DecisionAsk,
				RiskLevel: RiskMedium,
				RuleID:    r.ID(),
				Evidence:  p,
				Reason:    "命令需要人工审核：" + p + "。该操作可能合法，但存在风险。",
			}
		}
	}
	return nil
}
