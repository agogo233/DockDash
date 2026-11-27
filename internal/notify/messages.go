package notify

import "strings"

var messages = map[string]map[string]string{
	"en": {
		"updateAvailable":   "⚠️ Update Available",
		"updatesAvailable":  "⚠️ Updates Available",
		"updateEntry":       "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest": "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"testTitle":         "Test Notification",
		"testBody":          "Apprise notifications are configured correctly.",
	},
	"pt-BR": {
		"updateAvailable":   "⚠️ Atualização Disponível",
		"updatesAvailable":  "⚠️ Atualizações Disponíveis",
		"updateEntry":       "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest": "{{name}}: {{currentVersion}} → {{latestVersion}}",
		"testTitle":         "Notificação de Teste",
		"testBody":          "As notificações do Apprise estão configuradas corretamente.",
	},
	"zh-CN": {
		"updateAvailable":   "⚠️ 有可用更新",
		"updatesAvailable":  "⚠️ 有可用更新",
		"updateEntry":       "{{name}}:{{currentVersion}} → {{latestVersion}}",
		"updateEntryDigest": "{{name}}:{{currentVersion}} → {{latestVersion}}",
		"testTitle":         "测试通知",
		"testBody":          "Apprise 通知已正确配置。",
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
