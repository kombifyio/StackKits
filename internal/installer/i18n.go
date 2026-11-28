package installer

import (
	"fmt"
	"os"
	"strings"
)

// SupportedLanguages lists all 7 installer localization languages matching
// install.sh (en, de, es, fr, hi, ar, zh-Hans).
var SupportedLanguages = []string{"en", "de", "es", "fr", "hi", "ar", "zh-Hans"}

// ResolveLanguage resolves the operator's preferred installer locale from
// explicit flag, KOMBIFY_LANG, LC_ALL, LC_MESSAGES, or LANG (default "en").
func ResolveLanguage(explicit string) string {
	candidates := []string{
		explicit,
		os.Getenv("KOMBIFY_LANG"),
		os.Getenv("LC_ALL"),
		os.Getenv("LC_MESSAGES"),
		os.Getenv("LANG"),
	}
	for _, raw := range candidates {
		norm := normalizeLang(raw)
		if norm != "" {
			return norm
		}
	}
	return "en"
}

func normalizeLang(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "C") || strings.EqualFold(raw, "POSIX") {
		return ""
	}
	// Strip encoding / modifier (e.g. de_DE.UTF-8 -> de_DE).
	if cut := strings.IndexAny(raw, ".@"); cut >= 0 {
		raw = raw[:cut]
	}
	lower := strings.ToLower(strings.ReplaceAll(raw, "_", "-"))
	switch {
	case lower == "en" || strings.HasPrefix(lower, "en-"):
		return "en"
	case lower == "de" || strings.HasPrefix(lower, "de-"):
		return "de"
	case lower == "es" || strings.HasPrefix(lower, "es-"):
		return "es"
	case lower == "fr" || strings.HasPrefix(lower, "fr-"):
		return "fr"
	case lower == "hi" || strings.HasPrefix(lower, "hi-"):
		return "hi"
	case lower == "ar" || strings.HasPrefix(lower, "ar-"):
		return "ar"
	case lower == "zh" || lower == "zh-cn" || lower == "zh-sg" || strings.HasPrefix(lower, "zh-hans"):
		return "zh-Hans"
	default:
		return ""
	}
}

var messages = map[string]map[string]string{
	"en": {
		"title":        "StackKits Go Installer",
		"sudo_prompt":  "Root privileges are required for host prerequisites; invoking interactive sudo.",
		"plan_ready":   "Install plan verified (zero local docker build; single OpenTofu Stage-1 Core engine).",
		"next_apply":   "Run `stackkit apply` inside your workspace to converge the stack.",
		"invalid_kit":  "Unsupported kit %q; choose basement-kit, cloud-kit, or modern-homelab.",
		"retired_base": "Kit name `base-kit` is retired; use `basement-kit`.",
	},
	"de": {
		"title":        "StackKits Go-Installer",
		"sudo_prompt":  "Für Host-Voraussetzungen werden Root-Rechte benötigt; starte interaktives sudo.",
		"plan_ready":   "Installationsplan geprüft (kein lokaler docker build; OpenTofu Stage-1 Core-Engine).",
		"next_apply":   "Führe `stackkit apply` im Workspace aus, um den Stack zu starten.",
		"invalid_kit":  "Unbekanntes Kit %q; wähle basement-kit, cloud-kit oder modern-homelab.",
		"retired_base": "Der Kit-Name `base-kit` ist veraltet; nutze `basement-kit`.",
	},
	"es": {
		"title":        "Instalador Go de StackKits",
		"sudo_prompt":  "Se requieren privilegios root para preparar el host; ejecutando sudo interactivo.",
		"plan_ready":   "Plan de instalación verificado (sin docker build local; motor OpenTofu Stage-1).",
		"next_apply":   "Ejecuta `stackkit apply` en tu espacio de trabajo para iniciar el stack.",
		"invalid_kit":  "Kit no compatible %q; elige basement-kit, cloud-kit o modern-homelab.",
		"retired_base": "El nombre `base-kit` fue retirado; usa `basement-kit`.",
	},
	"fr": {
		"title":        "Installateur Go StackKits",
		"sudo_prompt":  "Des privilèges root sont requis pour préparer l'hôte ; lancement de sudo interactif.",
		"plan_ready":   "Plan d'installation vérifié (aucun docker build local ; moteur OpenTofu Stage-1).",
		"next_apply":   "Exécutez `stackkit apply` dans votre espace de travail pour démarrer la pile.",
		"invalid_kit":  "Kit non pris en charge %q ; choisissez basement-kit, cloud-kit ou modern-homelab.",
		"retired_base": "Le nom `base-kit` est retiré ; utilisez `basement-kit`.",
	},
	"hi": {
		"title":        "StackKits Go इंस्टॉलर",
		"sudo_prompt":  "होस्ट तैयारी के लिए root अधिकार आवश्यक हैं; इंटरैक्टिव sudo चलाया जा रहा है।",
		"plan_ready":   "इंस्टॉल योजना सत्यापित (कोई स्थानीय docker build नहीं; OpenTofu Stage-1 इंजन)।",
		"next_apply":   "स्टैक शुरू करने के लिए अपने वर्कस्पेस में `stackkit apply` चलाएँ।",
		"invalid_kit":  "असमर्थित किट %q; basement-kit, cloud-kit या modern-homelab चुनें।",
		"retired_base": "`base-kit` नाम हटाया जा चुका है; `basement-kit` उपयोग करें।",
	},
	"ar": {
		"title":        "مثبّت StackKits بلغة Go",
		"sudo_prompt":  "صلاحيات root مطلوبة لإعداد المضيف؛ جارٍ تشغيل sudo التفاعلي.",
		"plan_ready":   "تم التحقق من خطة التثبيت (دون بناء docker محلي؛ محرك OpenTofu Stage-1).",
		"next_apply":   "شغّل `stackkit apply` داخل مساحة العمل لتطبيق الحزمة.",
		"invalid_kit":  "الحزمة %q غير مدعومة؛ اختر basement-kit أو cloud-kit أو modern-homelab.",
		"retired_base": "تم إيقاف اسم `base-kit`؛ استخدم `basement-kit`.",
	},
	"zh-Hans": {
		"title":        "StackKits Go 安装程序",
		"sudo_prompt":  "准备主机环境需要 root 权限；正在调用交互式 sudo。",
		"plan_ready":   "安装计划已验证（无本地 docker build；统一 OpenTofu Stage-1 核心引擎）。",
		"next_apply":   "请在工作目录中运行 `stackkit apply` 以部署堆栈。",
		"invalid_kit":  "不支持的套件 %q；请选择 basement-kit、cloud-kit 或 modern-homelab。",
		"retired_base": "`base-kit` 名称已弃用；请使用 `basement-kit`。",
	},
}

// Msg formats a localized installer notice across the 7 supported languages.
func Msg(lang, key string, args ...any) string {
	table, ok := messages[lang]
	if !ok {
		table = messages["en"]
	}
	tmpl, ok := table[key]
	if !ok {
		tmpl = messages["en"][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(tmpl, args...)
	}
	return tmpl
}
