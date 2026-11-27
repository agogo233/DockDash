package notify

import "strings"

var messages = map[string]map[string]string{
	"en": {
		"updateAvailable":                 "⚠️ Update Available",
		"updatesAvailable":                "⚠️ Updates Available",
		"updateEntry":                     "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest":               "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"serviceDown":                     "⚠️ Service Down: {{name}}",
		"serviceDownBody":                 "{{name}} is no longer reachable.",
		"serviceRecovered":                "✅ Service Recovered: {{name}}",
		"serviceRecoveredBody":            "{{name}} is back online.",
		"serviceStatusChanges":            "Service Status Changes",
		"servicesDown":                    "⚠️ Down ({{total}})",
		"servicesRecovered":               "✅ Recovered ({{total}})",
		"cpuSpike":                        "⚠️ CPU Spike: {{name}}",
		"cpuSpikeBody":                    "{{name}} CPU usage is at {{percent}}%.",
		"cpuRecovered":                    "✅ CPU Recovered: {{name}}",
		"cpuRecoveredBody":                "{{name}} CPU usage has returned to normal.",
		"memorySpike":                     "⚠️ Memory Spike: {{name}}",
		"memorySpikeBody":                 "{{name}} memory usage is at {{percent}}%.",
		"memoryRecovered":                 "✅ Memory Recovered: {{name}}",
		"memoryRecoveredBody":             "{{name}} memory usage has returned to normal.",
		"certificateExpiring":             "⚠️ Certificate Expiring: {{name}}",
		"certificateExpiringBody":         "{{name}} ({{target}}) expires in {{days}} days on {{date}}.",
		"certificateError":                "❌ Certificate Error: {{name}}",
		"certificateErrorBody":            "{{name}} ({{target}}) has a TLS certificate problem: {{error}}",
		"certificateRecovered":            "✅ Certificate Recovered: {{name}}",
		"certificateRecoveredBody":        "{{name}} ({{target}}) is serving a valid TLS certificate again.",
		"certificateRenewed":              "✅ Certificate Renewed: {{name}}",
		"certificateRenewedBody":          "{{name}} ({{target}}) is serving a new certificate.",
		"certificateMismatch":             "⚠️ Certificate Deployment Mismatch: {{name}}",
		"certificateMismatchBody":         "{{name}} ({{target}}) is not serving the current CertVault certificate.",
		"certificateMismatchResolved":     "✅ Certificate Deployment Updated: {{name}}",
		"certificateMismatchResolvedBody": "{{name}} ({{target}}) now serves the current CertVault certificate.",
		"testTitle":                       "Test Notification",
		"testBody":                        "Apprise notifications are configured correctly.",
	},
	"pt-BR": {
		"updateAvailable":                 "⚠️ Atualização Disponível",
		"updatesAvailable":                "⚠️ Atualizações Disponíveis",
		"updateEntry":                     "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest":               "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"serviceDown":                     "⚠️ Serviço Fora do Ar: {{name}}",
		"serviceDownBody":                 "{{name}} não está mais acessível.",
		"serviceRecovered":                "✅ Serviço Recuperado: {{name}}",
		"serviceRecoveredBody":            "{{name}} está de volta online.",
		"serviceStatusChanges":            "Alterações de Status dos Serviços",
		"servicesDown":                    "⚠️ Fora do Ar ({{total}})",
		"servicesRecovered":               "✅ Recuperados ({{total}})",
		"cpuSpike":                        "⚠️ Pico de CPU: {{name}}",
		"cpuSpikeBody":                    "O uso de CPU de {{name}} está em {{percent}}%.",
		"cpuRecovered":                    "✅ CPU Normalizada: {{name}}",
		"cpuRecoveredBody":                "O uso de CPU de {{name}} voltou ao normal.",
		"memorySpike":                     "⚠️ Pico de Memória: {{name}}",
		"memorySpikeBody":                 "O uso de memória de {{name}} está em {{percent}}%.",
		"memoryRecovered":                 "✅ Memória Normalizada: {{name}}",
		"memoryRecoveredBody":             "O uso de memória de {{name}} voltou ao normal.",
		"certificateExpiring":             "⚠️ Certificado Expirando: {{name}}",
		"certificateExpiringBody":         "{{name}} ({{target}}) expira em {{days}} dias, em {{date}}.",
		"certificateError":                "❌ Erro de Certificado: {{name}}",
		"certificateErrorBody":            "{{name}} ({{target}}) tem um problema no certificado TLS: {{error}}",
		"certificateRecovered":            "✅ Certificado Recuperado: {{name}}",
		"certificateRecoveredBody":        "{{name}} ({{target}}) voltou a servir um certificado TLS válido.",
		"certificateRenewed":              "✅ Certificado Renovado: {{name}}",
		"certificateRenewedBody":          "{{name}} ({{target}}) está servindo um novo certificado.",
		"certificateMismatch":             "⚠️ Divergência de Certificado: {{name}}",
		"certificateMismatchBody":         "{{name}} ({{target}}) não está servindo o certificado atual do CertVault.",
		"certificateMismatchResolved":     "✅ Certificado Implantado: {{name}}",
		"certificateMismatchResolvedBody": "{{name}} ({{target}}) agora está servindo o certificado atual do CertVault.",
		"testTitle":                       "Notificação de Teste",
		"testBody":                        "As notificações do Apprise estão configuradas corretamente.",
	},
	"zh-CN": {
		"updateAvailable":                 "⚠️ 有可用更新",
		"updatesAvailable":                "⚠️ 有可用更新",
		"updateEntry":                     "{{name}}:{{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest":               "{{name}}:{{currentVersion}} → {{latestVersion}}",
		"serviceDown":                     "⚠️ 服务离线:{{name}}",
		"serviceDownBody":                 "{{name}} 已无法访问。",
		"serviceRecovered":                "✅ 服务恢复:{{name}}",
		"serviceRecoveredBody":            "{{name}} 已恢复在线。",
		"serviceStatusChanges":            "服务状态变更",
		"servicesDown":                    "⚠️ 离线 ({{total}})",
		"servicesRecovered":               "✅ 已恢复 ({{total}})",
		"cpuSpike":                        "⚠️ CPU 峰值:{{name}}",
		"cpuSpikeBody":                    "{{name}} 的 CPU 使用率为 {{percent}}%。",
		"cpuRecovered":                    "✅ CPU 已恢复:{{name}}",
		"cpuRecoveredBody":                "{{name}} 的 CPU 使用率已恢复正常。",
		"memorySpike":                     "⚠️ 内存峰值:{{name}}",
		"memorySpikeBody":                 "{{name}} 的内存使用率为 {{percent}}%。",
		"memoryRecovered":                 "✅ 内存已恢复:{{name}}",
		"memoryRecoveredBody":             "{{name}} 的内存使用率已恢复正常。",
		"certificateExpiring":             "⚠️ 证书即将到期:{{name}}",
		"certificateExpiringBody":         "{{name}} ({{target}}) 将于 {{date}} 到期,剩余 {{days}} 天。",
		"certificateError":                "❌ 证书错误:{{name}}",
		"certificateErrorBody":            "{{name}} ({{target}}) 的 TLS 证书存在问题:{{error}}",
		"certificateRecovered":            "✅ 证书已恢复:{{name}}",
		"certificateRecoveredBody":        "{{name}} ({{target}}) 已恢复正常提供 TLS 证书。",
		"certificateRenewed":              "✅ 证书已续期:{{name}}",
		"certificateRenewedBody":          "{{name}} ({{target}}) 正在提供新的 TLS 证书。",
		"certificateMismatch":             "⚠️ 证书部署不一致:{{name}}",
		"certificateMismatchBody":         "{{name}} ({{target}}) 未提供 CertVault 的当前证书。",
		"certificateMismatchResolved":     "✅ 证书部署已更新:{{name}}",
		"certificateMismatchResolvedBody": "{{name}} ({{target}}) 现已提供 CertVault 的当前证书。",
		"testTitle":                       "测试通知",
		"testBody":                        "Apprise 通知已正确配置。",
	},
}

func Message(locale, key string, values map[string]string) string {
	localeMessages := messages[locale]
	if localeMessages == nil {
		localeMessages = messages["en"]
	}
	template := localeMessages[key]
	if template == "" {
		template = messages["en"][key]
	}
	replacements := []string{}
	for name, value := range values {
		replacements = append(replacements, "{{"+name+"}}", value)
	}
	return strings.NewReplacer(replacements...).Replace(template)
}
