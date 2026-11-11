#!/bin/sh
# =============================================================================
# StackKits Basement Installer — full basement-kit deployment in one command.
# =============================================================================
# Usage: curl -sSL https://base.stackkit.cc | sh
#
# This installer carries you through the COMPLETE installation to a running
# homelab. It is a guidance layer over the stackkit CLI: every decision maps
# to a CLI parameter, and the same result is achievable with plain CLI verbs
# (see "CLI path" in the final summary or docs/INSTALLATION_PROCESSES.md).
#
# Modes (STACKKIT_INSTALL_MODE=auto|guided|expert; default auto):
#   auto    Quick Install: owner email and use cases, then defaults to a
#           running homelab. Files/Photos/Vault start checked in the picker.
#   guided  Same defaults; kept for existing automation.
#   expert  Same defaults; kept for existing automation.
# The installer asks only for the owner email and the use cases; every other
# decision is taken from its default, printed as one line, and overridden by
# the environment variable named below. Without a terminal it asks nothing and
# takes the same defaults; a terminal is required only where it asks.
#
# Steps:
#   1. Install stackkit CLI + basement-kit definitions  (via install.stackkit.cc)
#   2. Initialize basement-kit                          (native v2 init)
#   3. Prepare the container runtime (Docker install + group activation)
#   4. Build the local stackkit-server image            (registry fallback)
#   5. Generate + deploy the full homelab stack         (host preflight, apply)
#
# Environment variables (all optional; each pre-seeds one decision):
#   KOMBIFY_LANG           en | de | es | fr | hi | ar | zh-Hans for the
#                          installer questions and notices (fallback: LC_ALL,
#                          LC_MESSAGES, LANG; default en)
#   STACKKIT_INSTALL_MODE  auto | guided | expert
#   HOMELAB_DIR            Workspace (default: $HOME/my-homelab)
#   DOMAIN                 Own domain (default: local lab.home zone)
#   STACKKIT_NAME          Deployment contract ID (default: workspace name)
#   STACKKIT_ADMIN_EMAIL   Required owner email for logins (KOMBIFY_USER_EMAIL fallback)
#   STACKKIT_OWNER_USERNAME   Login username (default: derived from the email)
#   STACKKIT_USE_CASES     Comma-separated optional workloads; skip the picker.
#                          files,photos,vault are the L3 defaults; none = core only
#   STACKKIT_PLATFORM / STACKKIT_PAAS
#                          Workload runtime adapter: standalone-compose (default) | komodo | coolify
#                          (applies to the selected use cases)
#   STACKKIT_PLACEMENT     home | cloud; picks the kit instead of the detection
#   STACKKIT_SKIP_APPLY=1  Stop after generate; keep the deployment intent
#   STACKKIT_SERVER_IMAGE  Optional stackkit-server image override
#   STACKKIT_INSTALL_URL   Release installer (default: https://install.stackkit.cc)
#   CLOUDFLARE_API_TOKEN / CLOUDFLARE_EMAIL
#                          DNS credentials for custom public domains
#   STACKKIT_PLATFORM_* / COOLIFY_* / KOMODO_* / DOKPLOY_*
#                          Advanced override for an external existing PaaS
#
# Requirements: Linux or macOS, root/sudo access
# =============================================================================
set -eu

# BEGIN STACKKIT_BOOTSTRAP_SOURCE
# stdin has no script directory; never load a helper from the caller's cwd.
case "$0" in
  */base-install.sh|base-install.sh)
    _INSTALL_SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)
    # shellcheck source=install-host-bootstrap.sh
    . "$_INSTALL_SCRIPT_DIR/install-host-bootstrap.sh"
    ;;
  *) printf 'Use the complete entrypoint: curl -fsSL https://base.stackkit.cc | sh\n' >&2; exit 2 ;;
esac
# END STACKKIT_BOOTSTRAP_SOURCE

printf '\033[38;5;208m'
cat <<'BANNER'

     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|

BANNER
printf '\033[0m'
# Nested stackkit init/apply must not reprint this greeting. Progress lines
# are the operator signal that later steps are still working.
export STACKKIT_NO_BANNER=1

# --- Helpers ------------------------------------------------------------------

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m==> %s\033[0m\n' "$*"; }
warn()  { printf '\033[1;33m==> %s\033[0m\n' "$*"; }
err()   { printf '\033[1;31m==> %s\033[0m\n' "$*" >&2; }
die()   { err "$*"; exit 1; }
can_prompt() { [ -t 1 ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }

# Installer questions and their notices follow the person's language:
# KOMBIFY_LANG, then LC_ALL, LC_MESSAGES, LANG, else English. The value is
# case-insensitive and its language part ends at "_", ".", "-" or "@".
installer_msg_lang() {
  _locale=$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')
  case "$_locale" in
    zh|zh_cn*|zh_sg*|zh-hans*|zh_hans*) printf 'zh-Hans'; return 0 ;;
    zh_tw*|zh_hk*|zh_mo*|zh-hant*) printf 'en'; return 0 ;;
  esac
  _lang=${_locale%%[_.@-]*}
  case "$_lang" in
    en|de|es|fr|hi|ar) printf '%s' "$_lang" ;;
    *) printf 'en' ;;
  esac
}
KOMBIFY_MSG_LANG=$(installer_msg_lang "${KOMBIFY_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}}")

# kit_placement DETECTED_ENV prints the kit this host gets: an explicit
# STACKKIT_PLACEMENT wins in both directions, otherwise the detected network
# decides (public → cloud, home or unknown → home).
kit_placement() {
  case "${STACKKIT_PLACEMENT:-}" in
    home|cloud) printf '%s' "$STACKKIT_PLACEMENT"; return 0 ;;
    "") ;;
    *) printf "Unsupported STACKKIT_PLACEMENT '%s'. Expected home or cloud.\n" "$STACKKIT_PLACEMENT" >&2; return 2 ;;
  esac
  case "${1:-}" in
    vps|cloud) printf 'cloud' ;;
    *) printf 'home' ;;
  esac
}
# t KEY [VALUE] prints one localized installer message.
t() {
  _t_value="${2:-}"
  case "$KOMBIFY_MSG_LANG:$1" in
    de:email_intro) printf 'Die Homelab-Anmeldung braucht vor der Installation eine E-Mail-Adresse.' ;;
    es:email_intro) printf 'Los accesos del homelab necesitan tu correo electrónico antes de la instalación.' ;;
    fr:email_intro) printf 'Les connexions au homelab ont besoin de votre adresse e-mail avant l’installation.' ;;
    hi:email_intro) printf 'इंस्टॉल से पहले होमलैब लॉगिन के लिए आपका ईमेल पता चाहिए।' ;;
    ar:email_intro) printf 'تحتاج عمليات تسجيل الدخول إلى الـ homelab إلى بريدك الإلكتروني قبل التثبيت.' ;;
    zh-Hans:email_intro) printf '安装前需要您的电子邮件地址来创建 Homelab 登录。' ;;
    *:email_intro) printf 'Homelab logins need your email before install.' ;;
    de:email_prompt) printf 'E-Mail-Adresse: ' ;;
    es:email_prompt) printf 'Correo electrónico: ' ;;
    fr:email_prompt) printf 'Adresse e-mail : ' ;;
    hi:email_prompt) printf 'ईमेल पता: ' ;;
    ar:email_prompt) printf 'البريد الإلكتروني: ' ;;
    zh-Hans:email_prompt) printf '电子邮件地址：' ;;
    *:email_prompt) printf 'Email address: ' ;;
    de:email_invalid) printf 'Bitte eine gültige E-Mail-Adresse eingeben, damit Anmeldungen des Eigentümers angelegt werden können.' ;;
    es:email_invalid) printf 'Introduce un correo electrónico válido para poder crear los accesos de propietario.' ;;
    fr:email_invalid) printf 'Saisissez une adresse e-mail valide pour créer les connexions du propriétaire.' ;;
    hi:email_invalid) printf 'मालिक लॉगिन बनाने के लिए मान्य ईमेल पता दर्ज करें।' ;;
    ar:email_invalid) printf 'أدخل بريدًا إلكترونيًا صالحًا لإنشاء عمليات دخول المالك.' ;;
    zh-Hans:email_invalid) printf '请输入有效的电子邮件地址，以便创建所有者登录。' ;;
    *:email_invalid) printf 'Enter a valid email address so owner logins can be created.' ;;
    de:email_required) printf 'Die Homelab-Installation braucht die E-Mail-Adresse des Eigentümers. STACKKIT_ADMIN_EMAIL setzen oder den Installer in einem Terminal starten.' ;;
    es:email_required) printf 'La instalación del homelab necesita un correo de propietario. Define STACKKIT_ADMIN_EMAIL o ejecuta el instalador desde un terminal.' ;;
    fr:email_required) printf 'L’installation du homelab a besoin d’une adresse e-mail de propriétaire. Définissez STACKKIT_ADMIN_EMAIL ou lancez l’installateur depuis un terminal.' ;;
    hi:email_required) printf 'होमलैब इंस्टॉल के लिए मालिक का ईमेल चाहिए। STACKKIT_ADMIN_EMAIL सेट करें या इंस्टॉलर को टर्मिनल से चलाएँ।' ;;
    ar:email_required) printf 'يحتاج تثبيت الـ homelab إلى بريد المالك. اضبط STACKKIT_ADMIN_EMAIL أو شغّل المثبّت من طرفية.' ;;
    zh-Hans:email_required) printf 'Homelab 安装需要所有者电子邮件。请设置 STACKKIT_ADMIN_EMAIL，或在终端中运行安装程序。' ;;
    *:email_required) printf 'Homelab install needs an owner email. Set STACKKIT_ADMIN_EMAIL or run this installer from a terminal.' ;;
    de:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL muss eine gültige E-Mail-Adresse sein.' ;;
    es:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL debe ser un correo electrónico válido.' ;;
    fr:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL doit être une adresse e-mail valide.' ;;
    hi:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL एक मान्य ईमेल पता होना चाहिए।' ;;
    ar:email_env_invalid) printf 'يجب أن يكون STACKKIT_ADMIN_EMAIL بريدًا إلكترونيًا صالحًا.' ;;
    zh-Hans:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL 必须是有效的电子邮件地址。' ;;
    *:email_env_invalid) printf 'STACKKIT_ADMIN_EMAIL must be a valid email address.' ;;
    de:username_notice) printf 'Login-Benutzername: %s (mit STACKKIT_OWNER_USERNAME änderbar)' "$_t_value" ;;
    es:username_notice) printf 'Nombre de usuario: %s (cambia con STACKKIT_OWNER_USERNAME)' "$_t_value" ;;
    fr:username_notice) printf 'Nom d’utilisateur : %s (modifiable avec STACKKIT_OWNER_USERNAME)' "$_t_value" ;;
    hi:username_notice) printf 'लॉगिन उपयोगकर्ता नाम: %s (STACKKIT_OWNER_USERNAME से बदलें)' "$_t_value" ;;
    ar:username_notice) printf 'اسم مستخدم الدخول: %s (يمكن تغييره عبر STACKKIT_OWNER_USERNAME)' "$_t_value" ;;
    zh-Hans:username_notice) printf '登录用户名：%s（可通过 STACKKIT_OWNER_USERNAME 修改）' "$_t_value" ;;
    *:username_notice) printf 'Login username: %s (set STACKKIT_OWNER_USERNAME to change)' "$_t_value" ;;
    de:workspace_label) printf 'Arbeitsverzeichnis' ;;
    es:workspace_label) printf 'Directorio de trabajo' ;;
    fr:workspace_label) printf 'Répertoire de travail' ;;
    hi:workspace_label) printf 'कार्यस्थान' ;;
    ar:workspace_label) printf 'مساحة العمل' ;;
    zh-Hans:workspace_label) printf '工作目录' ;;
    *:workspace_label) printf 'Workspace' ;;
    de:stack_name_label) printf 'Stack-Name' ;;
    es:stack_name_label) printf 'Nombre del stack' ;;
    fr:stack_name_label) printf 'Nom du stack' ;;
    hi:stack_name_label) printf 'स्टैक नाम' ;;
    ar:stack_name_label) printf 'اسم الـ stack' ;;
    zh-Hans:stack_name_label) printf 'Stack 名称' ;;
    *:stack_name_label) printf 'Stack name' ;;
    de:platform_label) printf 'Plattform-Adapter' ;;
    es:platform_label) printf 'Adaptador de plataforma' ;;
    fr:platform_label) printf 'Adaptateur de plateforme' ;;
    hi:platform_label) printf 'प्लेटफ़ॉर्म अडैप्टर' ;;
    ar:platform_label) printf 'محوّل المنصة' ;;
    zh-Hans:platform_label) printf '平台适配器' ;;
    *:platform_label) printf 'Platform adapter' ;;
    de:mode_label) printf 'Installationsmodus' ;;
    es:mode_label) printf 'Modo de instalación' ;;
    fr:mode_label) printf 'Mode d’installation' ;;
    hi:mode_label) printf 'इंस्टॉल मोड' ;;
    ar:mode_label) printf 'وضع التثبيت' ;;
    zh-Hans:mode_label) printf '安装模式' ;;
    *:mode_label) printf 'Install mode' ;;
    de:use_cases_notice) printf 'Anwendungsfälle wählen (Pfeiltasten bewegen, Leertaste schaltet um, Enter bestätigt)' ;;
    es:use_cases_notice) printf 'Selecciona casos de uso (flechas para mover, espacio para marcar, Enter para confirmar)' ;;
    fr:use_cases_notice) printf 'Choisissez des cas d’usage (flèches pour naviguer, espace pour cocher, Entrée pour confirmer)' ;;
    hi:use_cases_notice) printf 'उपयोग केस चुनें (तीर घुमाते हैं, स्पेस चुनता है, Enter पुष्टि करता है)' ;;
    ar:use_cases_notice) printf 'اختر حالات الاستخدام (الأسهم للتنقل، المسافة للتبديل، Enter للتأكيد)' ;;
    zh-Hans:use_cases_notice) printf '选择用例（方向键移动，空格切换，Enter 确认）' ;;
    *:use_cases_notice) printf 'Select use cases (arrows move, space toggles, Enter confirms)' ;;
    de:use_cases_required) printf 'Vor der Installation muss ein Anwendungsfall gewählt werden. Zuerst die stackkit-CLI installieren oder STACKKIT_USE_CASES setzen.' ;;
    es:use_cases_required) printf 'Hay que seleccionar casos de uso antes de instalar. Instala primero la CLI de stackkit o define STACKKIT_USE_CASES.' ;;
    fr:use_cases_required) printf 'La sélection des cas d’usage est requise avant l’installation. Installez d’abord la CLI stackkit ou définissez STACKKIT_USE_CASES.' ;;
    hi:use_cases_required) printf 'इंस्टॉल से पहले उपयोग केस चुनना ज़रूरी है। पहले stackkit CLI इंस्टॉल करें या STACKKIT_USE_CASES सेट करें।' ;;
    ar:use_cases_required) printf 'اختيار حالات الاستخدام مطلوب قبل التثبيت. ثبّت stackkit CLI أولًا أو اضبط STACKKIT_USE_CASES.' ;;
    zh-Hans:use_cases_required) printf '安装前必须选择用例。请先安装 stackkit CLI，或设置 STACKKIT_USE_CASES。' ;;
    *:use_cases_required) printf 'Use-case selection is required before install. Install the stackkit CLI first, or set STACKKIT_USE_CASES.' ;;
    de:cloud_notice) printf 'Öffentlicher Server: Cloud Kit wird verwendet. Mit STACKKIT_PLACEMENT=home wird stattdessen das Basement Kit verwendet.' ;;
    es:cloud_notice) printf 'Servidor público: se continúa con Cloud Kit. Define STACKKIT_PLACEMENT=home para usar Basement Kit en su lugar.' ;;
    fr:cloud_notice) printf 'Serveur public : poursuite avec Cloud Kit. Définissez STACKKIT_PLACEMENT=home pour utiliser Basement Kit à la place.' ;;
    hi:cloud_notice) printf 'सार्वजनिक सर्वर: Cloud Kit के साथ जारी। Basement Kit के लिए STACKKIT_PLACEMENT=home सेट करें।' ;;
    ar:cloud_notice) printf 'خادم عام: المتابعة باستخدام Cloud Kit. اضبط STACKKIT_PLACEMENT=home لاستخدام Basement Kit بدلًا منه.' ;;
    zh-Hans:cloud_notice) printf '公共服务器：继续使用 Cloud Kit。设置 STACKKIT_PLACEMENT=home 可改用 Basement Kit。' ;;
    *:cloud_notice) printf 'Public server: continuing with Cloud Kit. Set STACKKIT_PLACEMENT=home to use Basement Kit instead.' ;;
    de:basement_notice) printf 'Heimnetz oder VPS konnte nicht erkannt werden: Basement Kit wird verwendet. Mit STACKKIT_PLACEMENT=cloud wird stattdessen das Cloud Kit verwendet.' ;;
    es:basement_notice) printf 'No se pudo distinguir entre red doméstica y VPS: se continúa con Basement Kit. Define STACKKIT_PLACEMENT=cloud para usar Cloud Kit en su lugar.' ;;
    fr:basement_notice) printf 'Impossible de distinguer réseau domestique et VPS : poursuite avec Basement Kit. Définissez STACKKIT_PLACEMENT=cloud pour utiliser Cloud Kit à la place.' ;;
    hi:basement_notice) printf 'होम नेटवर्क या VPS पहचाना नहीं गया: Basement Kit के साथ जारी। Cloud Kit के लिए STACKKIT_PLACEMENT=cloud सेट करें।' ;;
    ar:basement_notice) printf 'تعذّر تمييز الشبكة المنزلية من VPS: المتابعة باستخدام Basement Kit. اضبط STACKKIT_PLACEMENT=cloud لاستخدام Cloud Kit بدلًا منه.' ;;
    zh-Hans:basement_notice) printf '无法区分家庭网络和 VPS：继续使用 Basement Kit。设置 STACKKIT_PLACEMENT=cloud 可改用 Cloud Kit。' ;;
    *:basement_notice) printf 'Could not tell home vs VPS: continuing with Basement Kit. Set STACKKIT_PLACEMENT=cloud to use Cloud Kit instead.' ;;
    de:domain_public_notice) printf 'Keine eigene Domain angegeben; kombify.me wird für den öffentlichen Zugang verwendet.' ;;
    es:domain_public_notice) printf 'No se indicó un dominio propio; se usa kombify.me para el acceso público.' ;;
    fr:domain_public_notice) printf 'Aucun domaine personnalisé fourni ; kombify.me est utilisé pour l’accès public.' ;;
    hi:domain_public_notice) printf 'कोई कस्टम डोमेन नहीं दिया गया; सार्वजनिक पहुँच के लिए kombify.me उपयोग हो रहा है।' ;;
    ar:domain_public_notice) printf 'لم يُحدَّد نطاق مخصص؛ يُستخدم kombify.me للوصول العام.' ;;
    zh-Hans:domain_public_notice) printf '未提供自定义域名；公开访问使用 kombify.me。' ;;
    *:domain_public_notice) printf 'No custom domain given; using kombify.me for public access.' ;;
    de:domain_private_notice) printf 'Es wird die private Heimnetz-Adresszone *.lab.home verwendet.' ;;
    es:domain_private_notice) printf 'Se usa la zona de direcciones privadas *.lab.home.' ;;
    fr:domain_private_notice) printf 'Utilisation de la zone d’adresses privées *.lab.home.' ;;
    hi:domain_private_notice) printf 'निजी होम पता ज़ोन *.lab.home उपयोग हो रहा है।' ;;
    ar:domain_private_notice) printf 'يُستخدم نطاق العناوين المنزلية الخاصة *.lab.home.' ;;
    zh-Hans:domain_private_notice) printf '使用家庭私有地址域 *.lab.home。' ;;
    *:domain_private_notice) printf 'Using the private home address zone *.lab.home.' ;;
    de:deploy_notice) printf 'Die Bereitstellung startet jetzt; mit STACKKIT_SKIP_APPLY=1 stoppt sie nach generate.' ;;
    es:deploy_notice) printf 'Desplegando ahora; con STACKKIT_SKIP_APPLY=1 se detiene tras generate.' ;;
    fr:deploy_notice) printf 'Déploiement en cours ; STACKKIT_SKIP_APPLY=1 arrête après generate.' ;;
    hi:deploy_notice) printf 'अभी डिप्लॉय हो रहा है; generate के बाद रोकने के लिए STACKKIT_SKIP_APPLY=1 सेट करें।' ;;
    ar:deploy_notice) printf 'جارٍ النشر الآن؛ STACKKIT_SKIP_APPLY=1 يوقف بعد generate.' ;;
    zh-Hans:deploy_notice) printf '正在部署；设置 STACKKIT_SKIP_APPLY=1 可在 generate 后停止。' ;;
    *:deploy_notice) printf 'Deploying now; set STACKKIT_SKIP_APPLY=1 to stop after generate.' ;;
    de:deploy_skip_notice) printf 'apply wird wunschgemäß übersprungen; die Bereitstellung ist vorbereitet.' ;;
    es:deploy_skip_notice) printf 'Se omite apply a petición; la intención de despliegue está lista.' ;;
    fr:deploy_skip_notice) printf 'apply est ignoré sur demande ; l’intention de déploiement est prête.' ;;
    hi:deploy_skip_notice) printf 'अनुरोध पर apply छोड़ा गया; डिप्लॉयमेंट इंटेंट तैयार है।' ;;
    ar:deploy_skip_notice) printf 'تم تخطّي apply بناءً على الطلب؛ نية النشر جاهزة.' ;;
    zh-Hans:deploy_skip_notice) printf '已按请求跳过 apply；部署意图已准备好。' ;;
    *:deploy_skip_notice) printf 'Skipping apply on request; deployment intent is ready.' ;;
    *) printf '' ;;
  esac
}

# Native v2 writes stack-spec.yaml as canonical JSON ("kit":{"slug":"cloud-kit"});
# older workspaces use YAML ("stackkit: cloud-kit").
workspace_spec_kit() {
  _spec="${1:-}"
  [ -f "$_spec" ] || return 0
  for _kit in cloud-kit basement-kit modern-homelab; do
    if grep -Eq "(^|[[:space:]])(stackkit|slug):[[:space:]]*$_kit([[:space:]]|\$)|\"(stackkit|slug)\"[[:space:]]*:[[:space:]]*\"$_kit\"" "$_spec"; then
      printf '%s' "$_kit"
      return 0
    fi
  done
}

# `stackkit init` replaces an existing StackSpec only under compare-and-swap
# (--expected-spec-hash). Replacing a placeholder with the first real Owner
# keeps the previous intent; a rerun never changes the kit.
set_aside_previous_spec() {
  _spec="$HOMELAB_DIR/stack-spec.yaml"
  [ -f "$_spec" ] || return 0
  _kept="$_spec.replaced-$(date -u +%Y%m%dT%H%M%SZ)"
  mv "$_spec" "$_kept" || die "Could not move the previous StackSpec aside: $_spec"
  warn "Kept the previous StackSpec as $(basename "$_kept"); initializing a new one."
}

read_host_environment() {
  DETECTED_ENV="${STACKKIT_NETWORK_ENV:-}"
  PUBLIC_SERVER=""
  if [ -z "$DETECTED_ENV" ] && command -v stackkit >/dev/null 2>&1; then
    if stackkit host environment --help >/dev/null 2>&1; then
      _env_json=$(stackkit host environment --json 2>/dev/null || true)
      DETECTED_ENV=$(printf '%s' "$_env_json" | sed -n 's/.*"environment"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
      PUBLIC_SERVER=$(printf '%s' "$_env_json" | sed -n 's/.*"publicServer"[[:space:]]*:[[:space:]]*\(true\|false\).*/\1/p')
    fi
  fi
  [ -z "$DETECTED_ENV" ] && DETECTED_ENV="unknown"
  if [ "$DETECTED_ENV" = "unknown" ] && [ "$PUBLIC_SERVER" = "true" ]; then
    DETECTED_ENV="vps"
  fi
  refine_home_network_detection
}

env_value() { eval "printf '%s' \"\${$1:-}\""; }
first_env_value() {
  for _stackkit_key in "$@"; do
    _stackkit_value=$(env_value "$_stackkit_key")
    if [ -n "$_stackkit_value" ]; then
      printf '%s' "$_stackkit_value"
      return 0
    fi
  done
  return 0
}
json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

valid_owner_email() {
  _email=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  case "$_email" in
    *[[:space:]]*|*"@"*"@"*) return 1 ;;
    *@*.*) return 0 ;;
    *) return 1 ;;
  esac
}

owner_username_from_email() {
  printf '%s' "$1" | sed 's/@.*//' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/^[.-]*//; s/[.-]*$//; s/--*/-/g' | cut -c1-32
}

is_placeholder_owner_email() {
  [ "$1" = "owner@home.test" ]
}

# Generated PocketID placeholders and missing owner.json are not a real account.
# Resume still asks for email and use cases in that case.
workspace_needs_owner_identity() {
  _owner_json="$HOMELAB_DIR/.stackkit/custody/owner.json"
  if [ ! -f "$_owner_json" ]; then
    return 0
  fi
  if grep -Eq '"email"[[:space:]]*:[[:space:]]*"owner@home\.test"' "$_owner_json"; then
    return 0
  fi
  return 1
}

# Owner logins cannot be created from a generated placeholder. Email is
# required before init; the username is suggested from the local part.
collect_owner_identity() {
  if [ -z "${ADMIN_EMAIL:-}" ]; then
    ADMIN_EMAIL="${STACKKIT_ADMIN_EMAIL:-${KOMBIFY_USER_EMAIL:-}}"
  fi
  if is_placeholder_owner_email "$ADMIN_EMAIL"; then
    ADMIN_EMAIL=""
  fi
  if [ -z "$ADMIN_EMAIL" ]; then
    if can_prompt; then
      echo ""
      echo "  $(t email_intro)"
      while true; do
        printf '  %s' "$(t email_prompt)" >/dev/tty
        read -r ADMIN_EMAIL </dev/tty || die "$(t email_required)"
        ADMIN_EMAIL=$(printf '%s' "$ADMIN_EMAIL" | tr -d '\r')
        if valid_owner_email "$ADMIN_EMAIL" && ! is_placeholder_owner_email "$ADMIN_EMAIL"; then
          break
        fi
        echo "  $(t email_invalid)"
      done
    else
      die "$(t email_required)"
    fi
  fi
  if ! valid_owner_email "$ADMIN_EMAIL"; then
    die "$(t email_env_invalid)"
  fi
  if [ -z "${OWNER_USERNAME:-}" ]; then
    OWNER_USERNAME="${STACKKIT_OWNER_USERNAME:-}"
  fi
  _username_derived=""
  if [ -z "$OWNER_USERNAME" ]; then
    OWNER_USERNAME=$(owner_username_from_email "$ADMIN_EMAIL")
    [ -n "$OWNER_USERNAME" ] || OWNER_USERNAME="owner"
    _username_derived="1"
  fi
  OWNER_USERNAME=$(printf '%s' "$OWNER_USERNAME" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/^[.-]*//; s/[.-]*$//; s/--*/-/g' | cut -c1-32)
  [ -n "$OWNER_USERNAME" ] || die "Login username cannot be empty."
  if [ -n "$_username_derived" ] && can_prompt; then
    info "$(t username_notice "$OWNER_USERNAME")"
  fi
  BOOTSTRAP_OWNER="true"
}

# --- Mode resolution ------------------------------------------------------------

INSTALL_MODE=$(printf '%s' "${STACKKIT_INSTALL_MODE:-}" | tr '[:upper:]' '[:lower:]')
case "$INSTALL_MODE" in
  quick) warn "STACKKIT_INSTALL_MODE=quick selected; using auto."; INSTALL_MODE="auto" ;;
  wizard|detailed) warn "STACKKIT_INSTALL_MODE=$INSTALL_MODE selected; using expert."; INSTALL_MODE="expert" ;;
  auto|guided|expert|"") ;;
  *) die "Unsupported STACKKIT_INSTALL_MODE '$INSTALL_MODE'. Expected auto, guided, or expert." ;;
esac

if [ -z "$INSTALL_MODE" ]; then
  INSTALL_MODE="auto"
fi
info "$(t mode_label): $INSTALL_MODE"

ADMIN_EMAIL=""
OWNER_USERNAME=""
BOOTSTRAP_OWNER=""

# --- Decision collection --------------------------------------------------------
# Environment-provided values are never re-asked in any mode.

HOMELAB_DIR_SET="${HOMELAB_DIR:+1}"
HOMELAB_DIR="${HOMELAB_DIR:-$HOME/my-homelab}"
info "$(t workspace_label): $HOMELAB_DIR"
installer_workspace_begin

RESUME_EXISTING=0
RESUME_INCOMPLETE=0
if workspace_has_deployment_intent; then
  RESUME_EXISTING=1
  if workspace_apply_incomplete; then
    RESUME_INCOMPLETE=1
    warn "Workspace $HOMELAB_DIR has a previous incomplete install; preparing the host and resuming apply."
  else
    warn "Workspace $HOMELAB_DIR already has StackKits intent; skipping init and resuming apply."
  fi
  if workspace_needs_owner_identity; then
    warn "Workspace owner is not a real account yet; email and use cases are required before install."
  fi
fi

DOMAIN_VALUE="${DOMAIN:-}"
# Domain and kit placement are decided after the CLI is installed so we can
# observe whether this host is a home network or a public server.

if [ "$RESUME_EXISTING" != "1" ] || workspace_needs_owner_identity; then
  collect_owner_identity
fi

STACK_NAME="${STACKKIT_NAME:-}"
USE_CASES="${STACKKIT_USE_CASES:-}"
PLATFORM_SELECTION=$(printf '%s' "${STACKKIT_PLATFORM:-${STACKKIT_PAAS:-}}" | tr '[:upper:]' '[:lower:]')

if [ "$INSTALL_MODE" = "expert" ]; then
  if [ -z "${STACKKIT_NAME:-}" ]; then
    _default_name=$(basename "$HOMELAB_DIR")
    info "$(t stack_name_label): $_default_name"
  fi
fi

[ "$USE_CASES" = "none" ] && USE_CASES=""

case "$PLATFORM_SELECTION" in
  ""|coolify|komodo|standalone-compose) ;;
  dokploy) die "Platform 'dokploy' is draft-only and not installable through this installer." ;;
  *) die "Unsupported platform '$PLATFORM_SELECTION'. Expected standalone-compose, komodo or coolify." ;;
esac
if [ -n "$PLATFORM_SELECTION" ] && [ -z "$USE_CASES" ]; then
  warn "STACKKIT_PLATFORM=$PLATFORM_SELECTION applies to selected use cases; none are selected, so the platform choice is recorded for the external-PaaS override only."
fi

# Legacy v0.6 knobs: accepted for compatibility, no longer part of native v2 init.
if [ -n "${STACKKIT_MODE:-}" ]; then
  warn "STACKKIT_MODE is a retired v0.6 knob; native v2 install mode is CUE-owned (ignored)."
fi
if [ -n "${STACKKIT_SERVICE_PROFILE:-}" ]; then
  warn "STACKKIT_SERVICE_PROFILE is a retired v0.6 knob; select use cases instead (ignored)."
fi

# --- External platform override helpers (unchanged advanced path) -----------------

platform_config_env_present() {
  for _stackkit_key in \
    STACKKIT_PLATFORM_ENDPOINT STACKKIT_PLATFORM_TOKEN \
    STACKKIT_PLATFORM_API_KEY STACKKIT_PLATFORM_API_SECRET \
    STACKKIT_PLATFORM_ENVIRONMENT_ID STACKKIT_PLATFORM_ENVIRONMENT_NAME \
    STACKKIT_PLATFORM_SERVER_ID STACKKIT_PLATFORM_SERVER_UUID \
    STACKKIT_PLATFORM_PROJECT_UUID STACKKIT_PLATFORM_ENVIRONMENT_UUID STACKKIT_PLATFORM_DESTINATION_UUID \
    DOKPLOY_API_URL DOKPLOY_API_KEY DOKPLOY_ENVIRONMENT_ID DOKPLOY_SERVER_ID \
    KOMODO_API_URL KOMODO_API_KEY KOMODO_API_SECRET KOMODO_SERVER_ID \
    COOLIFY_API_URL COOLIFY_API_TOKEN COOLIFY_ENVIRONMENT_NAME COOLIFY_SERVER_UUID \
    COOLIFY_PROJECT_UUID COOLIFY_ENVIRONMENT_UUID COOLIFY_DESTINATION_UUID; do
    if [ -n "$(env_value "$_stackkit_key")" ]; then
      return 0
    fi
  done
  return 1
}
write_platform_json_field() {
  _stackkit_json_name="$1"
  _stackkit_json_value="$2"
  _stackkit_json_file="$3"
  if [ -z "$_stackkit_json_value" ]; then
    return 0
  fi
  if [ "$PLATFORM_JSON_HAS_FIELDS" = "true" ]; then
    printf ',\n' >> "$_stackkit_json_file"
  else
    PLATFORM_JSON_HAS_FIELDS="true"
  fi
  printf '  "%s": "%s"' "$_stackkit_json_name" "$(json_escape "$_stackkit_json_value")" >> "$_stackkit_json_file"
}
persist_platform_config() {
  if ! platform_config_env_present; then
    return 0
  fi

  PLATFORM_NAME="$PLATFORM_SELECTION"
  if [ -z "$PLATFORM_NAME" ]; then
    if [ -n "${COOLIFY_API_URL:-}" ] || [ -n "${COOLIFY_API_TOKEN:-}" ]; then
      PLATFORM_NAME="coolify"
    elif [ -n "${KOMODO_API_URL:-}" ] || [ -n "${KOMODO_API_KEY:-}" ] || [ -n "${KOMODO_API_SECRET:-}" ]; then
      PLATFORM_NAME="komodo"
    elif [ -n "${DOKPLOY_API_URL:-}" ] || [ -n "${DOKPLOY_API_KEY:-}" ]; then
      PLATFORM_NAME="dokploy"
    else
      PLATFORM_NAME="coolify"
    fi
  fi
  PLATFORM_TOKEN=""
  PLATFORM_API_KEY=""
  PLATFORM_API_SECRET=""
  PLATFORM_ENVIRONMENT_ID=""
  PLATFORM_SERVER_ID=""

  case "$PLATFORM_NAME" in
    coolify)
      PLATFORM_ENDPOINT=$(first_env_value COOLIFY_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_TOKEN=$(first_env_value COOLIFY_API_TOKEN STACKKIT_PLATFORM_TOKEN)
      PLATFORM_ENVIRONMENT_ID=$(first_env_value COOLIFY_ENVIRONMENT_NAME STACKKIT_PLATFORM_ENVIRONMENT_NAME)
      PLATFORM_SERVER_ID=$(first_env_value COOLIFY_SERVER_UUID STACKKIT_PLATFORM_SERVER_UUID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    dokploy)
      PLATFORM_ENDPOINT=$(first_env_value DOKPLOY_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_TOKEN=$(first_env_value DOKPLOY_API_KEY STACKKIT_PLATFORM_TOKEN)
      PLATFORM_ENVIRONMENT_ID=$(first_env_value DOKPLOY_ENVIRONMENT_ID STACKKIT_PLATFORM_ENVIRONMENT_ID)
      PLATFORM_SERVER_ID=$(first_env_value DOKPLOY_SERVER_ID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    komodo)
      PLATFORM_ENDPOINT=$(first_env_value KOMODO_API_URL STACKKIT_PLATFORM_ENDPOINT)
      PLATFORM_API_KEY=$(first_env_value KOMODO_API_KEY STACKKIT_PLATFORM_API_KEY STACKKIT_PLATFORM_TOKEN)
      PLATFORM_API_SECRET=$(first_env_value KOMODO_API_SECRET STACKKIT_PLATFORM_API_SECRET)
      PLATFORM_SERVER_ID=$(first_env_value KOMODO_SERVER_ID STACKKIT_PLATFORM_SERVER_ID)
      ;;
    *)
      die "Unsupported STACKKIT_PLATFORM '$PLATFORM_NAME'. Expected coolify, komodo, or draft-only dokploy."
      ;;
  esac

  if [ "$PLATFORM_NAME" = "komodo" ]; then
    if [ -z "$PLATFORM_ENDPOINT" ] || [ -z "$PLATFORM_API_KEY" ] || [ -z "$PLATFORM_API_SECRET" ]; then
      die "Komodo platform config override is incomplete. Provide endpoint, api key, and api secret for an external existing Komodo."
    fi
  elif [ -z "$PLATFORM_ENDPOINT" ] || [ -z "$PLATFORM_TOKEN" ]; then
    die "Platform config override is incomplete. Remove the partial STACKKIT_PLATFORM_* / provider env vars, or provide endpoint and token for an external existing PaaS."
  fi

  PLATFORM_PROJECT_UUID=$(first_env_value COOLIFY_PROJECT_UUID STACKKIT_PLATFORM_PROJECT_UUID)
  PLATFORM_ENVIRONMENT_UUID=$(first_env_value COOLIFY_ENVIRONMENT_UUID STACKKIT_PLATFORM_ENVIRONMENT_UUID)
  PLATFORM_DESTINATION_UUID=$(first_env_value COOLIFY_DESTINATION_UUID STACKKIT_PLATFORM_DESTINATION_UUID)

  mkdir -p "$HOMELAB_DIR/.stackkit"
  chmod 700 "$HOMELAB_DIR/.stackkit" 2>/dev/null || true
  PLATFORM_JSON="$HOMELAB_DIR/.stackkit/platform.json"
  PLATFORM_JSON_TMP="$PLATFORM_JSON.tmp"
  PLATFORM_JSON_HAS_FIELDS="false"
  : > "$PLATFORM_JSON_TMP"
  chmod 600 "$PLATFORM_JSON_TMP" 2>/dev/null || true
  printf '{\n' > "$PLATFORM_JSON_TMP"
  write_platform_json_field "platform" "$PLATFORM_NAME" "$PLATFORM_JSON_TMP"
  write_platform_json_field "endpoint" "$PLATFORM_ENDPOINT" "$PLATFORM_JSON_TMP"
  write_platform_json_field "token" "$PLATFORM_TOKEN" "$PLATFORM_JSON_TMP"
  write_platform_json_field "apiKey" "$PLATFORM_API_KEY" "$PLATFORM_JSON_TMP"
  write_platform_json_field "apiSecret" "$PLATFORM_API_SECRET" "$PLATFORM_JSON_TMP"
  write_platform_json_field "environmentId" "$PLATFORM_ENVIRONMENT_ID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "serverId" "$PLATFORM_SERVER_ID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "projectUuid" "$PLATFORM_PROJECT_UUID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "environmentUuid" "$PLATFORM_ENVIRONMENT_UUID" "$PLATFORM_JSON_TMP"
  write_platform_json_field "destinationUuid" "$PLATFORM_DESTINATION_UUID" "$PLATFORM_JSON_TMP"
  printf '\n}\n' >> "$PLATFORM_JSON_TMP"
  mv "$PLATFORM_JSON_TMP" "$PLATFORM_JSON"
  chmod 600 "$PLATFORM_JSON" 2>/dev/null || true
  ok "  Platform API config persisted to $PLATFORM_JSON"
}

configure_stackkit_server_image() {
  if [ -n "${STACKKIT_SERVER_IMAGE:-}" ]; then
    ok "  StackKit API image: $STACKKIT_SERVER_IMAGE"
    export STACKKIT_SERVER_IMAGE
    return 0
  fi

  if ! command -v stackkit-server >/dev/null 2>&1; then
    warn "stackkit-server binary not installed; falling back to the configured registry image."
    return 0
  fi

  DOCKER_CMD="docker"
  if ! docker info >/dev/null 2>&1; then
    if [ "${STACKKIT_RUN:-}" = "sg" ] && sg docker -c "docker info" >/dev/null 2>&1; then
      DOCKER_CMD="sg"
    elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
      DOCKER_CMD="sudo -n docker"
    else
      warn "Docker is not reachable for local stackkit-server image build; falling back to the configured registry image."
      return 0
    fi
  fi

  STACKKIT_SERVER_LOCAL_IMAGE="${STACKKIT_SERVER_LOCAL_IMAGE:-stackkit-server:local}"
  STACKKIT_SERVER_IMAGE_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t stackkit-server-image)

  cp "$(command -v stackkit-server)" "$STACKKIT_SERVER_IMAGE_DIR/stackkit-server"
  chmod +x "$STACKKIT_SERVER_IMAGE_DIR/stackkit-server" 2>/dev/null || true

  STACKKIT_SERVER_CA_CERTS=""
  for STACKKIT_SERVER_CA_CANDIDATE in \
    /etc/ssl/certs/ca-certificates.crt \
    /etc/pki/tls/certs/ca-bundle.crt \
    /etc/ssl/ca-bundle.pem \
    /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem; do
    if [ -r "$STACKKIT_SERVER_CA_CANDIDATE" ]; then
      cp "$STACKKIT_SERVER_CA_CANDIDATE" "$STACKKIT_SERVER_IMAGE_DIR/ca-certificates.crt"
      STACKKIT_SERVER_CA_CERTS=1
      break
    fi
  done
  if [ -z "$STACKKIT_SERVER_CA_CERTS" ]; then
    warn "Host CA bundle not found; local stackkit-server image will use an empty CA bundle."
    : > "$STACKKIT_SERVER_IMAGE_DIR/ca-certificates.crt"
  fi
  mkdir -p "$STACKKIT_SERVER_IMAGE_DIR/tmp" "$STACKKIT_SERVER_IMAGE_DIR/var/tmp"
  : > "$STACKKIT_SERVER_IMAGE_DIR/tmp/.keep"
  : > "$STACKKIT_SERVER_IMAGE_DIR/var/tmp/.keep"

  cat > "$STACKKIT_SERVER_IMAGE_DIR/Dockerfile" <<'EOF'
FROM scratch
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY tmp /tmp
COPY var /var
COPY stackkit-server /usr/local/bin/stackkit-server
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/usr/local/bin/stackkit-server"]
EOF

  info "Building local stackkit-server image ($STACKKIT_SERVER_LOCAL_IMAGE)"
  if [ "$DOCKER_CMD" = "sg" ]; then
    sg docker -c "docker build -q -t $STACKKIT_SERVER_LOCAL_IMAGE $STACKKIT_SERVER_IMAGE_DIR" >/dev/null       || die "Local stackkit-server image build failed. Set STACKKIT_SERVER_IMAGE to a reachable image and retry."
  elif ! $DOCKER_CMD build -q -t "$STACKKIT_SERVER_LOCAL_IMAGE" "$STACKKIT_SERVER_IMAGE_DIR" >/dev/null; then
    die "Local stackkit-server image build failed. Set STACKKIT_SERVER_IMAGE to a reachable image and retry."
  fi
  rm -rf "$STACKKIT_SERVER_IMAGE_DIR"
  STACKKIT_SERVER_IMAGE_DIR=""
  STACKKIT_SERVER_IMAGE="$STACKKIT_SERVER_LOCAL_IMAGE"
  export STACKKIT_SERVER_IMAGE
  ok "  StackKit API image: $STACKKIT_SERVER_IMAGE"
}

stop_on_hypervisor_host
installer_save_inputs

# --- Step 1: Install CLI + basement-kit definitions -------------------------------

info "Step 1/5 -- Installing stackkit CLI + basement-kit"
installer_phase install-cli
STACKKIT_INSTALL_URL="${STACKKIT_INSTALL_URL:-https://install.stackkit.cc}"
STACKKIT_NO_BANNER=1 installer_run_entry "$STACKKIT_INSTALL_URL" basement-kit
ok "  stackkit $(stackkit version 2>/dev/null | head -1) installed"

SELECTED_KIT="basement-kit"
read_host_environment
info "Detected network: $DETECTED_ENV"

PLACEMENT=$(kit_placement "$DETECTED_ENV") || exit 2
_existing_kit=$(workspace_spec_kit "$HOMELAB_DIR/stack-spec.yaml")
if [ "$RESUME_EXISTING" = "1" ]; then
  # Saved intent wins over network detection. A rerun is never a kit migration
  # and never authorization to remove the existing applications or data.
  case "$_existing_kit" in
    basement-kit) PLACEMENT="home" ;;
    cloud-kit)
      info "Keeping the saved Cloud Kit intent; continuing with its installer."
      export STACKKIT_PLACEMENT=cloud
      installer_phase handoff-cloud
      installer_run_entry "${STACKKIT_CLOUD_INSTALL_URL:-https://cloud.stackkit.cc}"
      exit $?
      ;;
    *) die "Cannot resume this workspace through the Basement installer. Inspect stackkit validate; use the entrypoint for its saved kit." ;;
  esac
fi

if [ "$RESUME_EXISTING" != "1" ]; then
  # The notice explains a default taken from detection; an explicit
  # STACKKIT_PLACEMENT was the person's own choice and needs no line.
  case "$DETECTED_ENV:$PLACEMENT" in
    vps:cloud|cloud:cloud) info "$(t cloud_notice)" ;;
    unknown:home) info "$(t basement_notice)" ;;
  esac
  if [ "$PLACEMENT" = "cloud" ]; then
    SELECTED_KIT="cloud-kit"
    export STACKKIT_PLACEMENT=cloud
    if [ -z "$HOMELAB_DIR_SET" ]; then
      if [ "$RESUME_EXISTING" != "1" ]; then
        HOMELAB_DIR="$HOME/my-cloud-homelab"
        installer_workspace_begin
      fi
    fi
    info "Using Cloud Kit on this host"
    STACKKIT_NO_BANNER=1 installer_run_entry "$STACKKIT_INSTALL_URL" cloud-kit
    if workspace_has_deployment_intent; then
      info "Preparing Cloud execution-channel access for this host"
      prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
      ok "  Cloud execution-channel account is ready"
    fi
    case "$DOMAIN_VALUE" in
      ""|home|lab.home|home.localhost|homelab|stack.home|home.lab|home.kombify)
        DOMAIN_VALUE=""
        ;;
    esac
  fi

  if [ -z "$DOMAIN_VALUE" ]; then
    if [ "$SELECTED_KIT" = "cloud-kit" ] || [ "$DETECTED_ENV" = "vps" ] || [ "$DETECTED_ENV" = "cloud" ]; then
      DOMAIN_VALUE="kombify.me"
      info "$(t domain_public_notice)"
    else
      DOMAIN_VALUE="lab.home"
      info "$(t domain_private_notice)"
    fi
  fi
  # A bare "home" is a public suffix no browser shares a login session across;
  # StackKits refuses it, so an old DOMAIN=home maps to the lab.home default.
  if [ "$SELECTED_KIT" = "basement-kit" ] && [ "$DOMAIN_VALUE" = "home" ]; then
    DOMAIN_VALUE="lab.home"
    info "DOMAIN=home is a public suffix without shared logins; using lab.home"
  fi
  if [ "$SELECTED_KIT" = "basement-kit" ] && [ "$DOMAIN_VALUE" = "lab.home" ]; then
    case "$DETECTED_ENV" in
      vps|cloud)
        DOMAIN_VALUE="kombify.me"
        info "Local *.lab.home addresses are not reachable on this public server; using kombify.me"
        ;;
    esac
  fi
fi

pick_use_cases() {
  if [ "$RESUME_EXISTING" = "1" ] && ! workspace_needs_owner_identity; then
    return 0
  fi
  if [ "${STACKKIT_USE_CASES+x}" = "x" ]; then
    USE_CASES="${STACKKIT_USE_CASES}"
    [ "$USE_CASES" = "none" ] && USE_CASES=""
    return 0
  fi
  if can_prompt && command -v stackkit >/dev/null 2>&1 && stackkit use-cases pick --help >/dev/null 2>&1; then
    info "$(t use_cases_notice)"
    USE_CASES=$(stackkit use-cases pick --print </dev/tty) || die "Use-case selection canceled"
    USE_CASES=$(printf '%s\n' "$USE_CASES" | tr -d '\r' | tail -n 1)
  elif can_prompt; then
    die "$(t use_cases_required)"
  else
    USE_CASES="files,photos,vault"
  fi
}

# --- Step 2: Initialize selected kit ----------------------------------------------

pick_use_cases
if [ "$INSTALL_MODE" = "expert" ] && [ -n "$USE_CASES" ] && [ -z "$PLATFORM_SELECTION" ]; then
  PLATFORM_SELECTION="standalone-compose"
  info "$(t platform_label): $PLATFORM_SELECTION"
fi
info "Step 2/5 -- Initializing $SELECTED_KIT"
installer_phase init
mkdir -p "$HOMELAB_DIR"
cd "$HOMELAB_DIR"

if [ "$RESUME_EXISTING" = "1" ] && ! workspace_needs_owner_identity; then
  stackkit validate
  ok "  existing workspace validated in $HOMELAB_DIR"
  if [ "$RESUME_INCOMPLETE" = "1" ] && [ "$SELECTED_KIT" = "cloud-kit" ]; then
    prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
    ok "  host prepared for Cloud Kit apply"
  fi
else
  collect_owner_identity
  installer_save_intent
  set_aside_previous_spec
  # Materialize the release catalog defaults as explicit native intent.
  # Existing workspaces above keep their persisted alternatives and adapters.
  set -- init "$SELECTED_KIT" --catalog-defaults --non-interactive --owner-source=local
  if [ -n "$DOMAIN_VALUE" ]; then
    set -- "$@" --domain "$DOMAIN_VALUE"
  fi
  if [ -n "$STACK_NAME" ]; then
    set -- "$@" --name "$STACK_NAME"
  fi
  set -- "$@" --owner-email "$ADMIN_EMAIL" --owner-username "$OWNER_USERNAME"
  set -- "$@" --use-case "$USE_CASES"
  if [ -n "$USE_CASES" ]; then
    if [ -n "$PLATFORM_SELECTION" ]; then
      set -- "$@" --platform "$PLATFORM_SELECTION"
    fi
  fi
  stackkit "$@"
  stackkit validate
  ok "  $SELECTED_KIT initialized and validated in $HOMELAB_DIR"
fi


# --- Step 3: Container runtime (Docker) -----------------------------------------
# `stackkit prepare` is external-host-only on the native v2 line; the local
# container runtime is bootstrapped here. `stackkit apply` would also install
# Docker, but the shell additionally activates the docker group for this
# session so non-root runs work without a re-login.

STACKKIT_RUN="direct"
run_stackkit() {
  if [ "$STACKKIT_RUN" = "sg" ]; then
    sg docker -c "stackkit $*"
  else
    stackkit "$@"
  fi
}

report_stackkit_failure() {
  err "stackkit $* failed."
  echo "  Retry in $HOMELAB_DIR: stackkit apply"
  echo "  Inspect: stackkit verify --json"
  echo "  Logs:    stackkit logs latest --json"
  exit 1
}

run_stackkit_step() {
  run_stackkit "$@" || report_stackkit_failure "$@"
}

ensure_docker() {
  if [ "${STACKKIT_SKIP_DOCKER_BOOTSTRAP:-}" = "1" ]; then
    warn "Docker bootstrap skipped (STACKKIT_SKIP_DOCKER_BOOTSTRAP=1)."
    return 0
  fi
  if docker info >/dev/null 2>&1; then
    ok "  Docker is ready"
    return 0
  fi
  if ! command -v docker >/dev/null 2>&1; then
    info "Installing Docker (get.docker.com)"
    if [ "$(id -u)" -eq 0 ]; then
      curl -fsSL https://get.docker.com | sh || die "Docker installation failed."
    else
      curl -fsSL https://get.docker.com | sudo sh || die "Docker installation failed."
    fi
  fi
  if [ "$(id -u)" -eq 0 ]; then
    systemctl enable --now docker >/dev/null 2>&1 || true
  else
    sudo systemctl enable --now docker >/dev/null 2>&1 || true
  fi
  if docker info >/dev/null 2>&1; then
    ok "  Docker is ready"
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    sudo usermod -aG docker "$(id -un)" 2>/dev/null || true
    if sg docker -c "docker info" >/dev/null 2>&1; then
      STACKKIT_RUN="sg"
      ok "  Docker is ready (docker group activated for this session; new shells have it permanently)"
      return 0
    fi
  fi
  die "Docker is installed but not reachable. Re-login (docker group membership) or start the daemon, then continue: cd $HOMELAB_DIR && stackkit generate && stackkit apply"
}

# provision_storage_roots: the Core host-bootstrap executor creates the
# spec-declared storage roots (0750) and runs as the invoking user; on
# non-root installs the parents under /opt etc. need root once. Pre-create
# them with the invoking user as owner so apply stays user-land.
provision_storage_roots() {
  [ -f "$HOMELAB_DIR/stack-spec.yaml" ] || return 0
  _roots=$(grep -o '"storage":{[^}]*}' "$HOMELAB_DIR/stack-spec.yaml" | grep -o '"/[^"]*"' | tr -d '"' || true)
  for _root in $_roots; do
    case "$_root" in
      /opt/*|/srv/*|/mnt/*|/media/*|/var/lib/stackkits/*) ;;
      *) continue ;;
    esac
    if [ "$(id -u)" -eq 0 ]; then
      install -d -m 0750 "$_root"
    else
      sudo install -d -m 0750 -o "$(id -un)" -g "$(id -gn)" "$_root"
    fi
  done
}

info "Step 3/5 -- Preparing container runtime (Docker)"
installer_phase prepare-runtime
ensure_docker
provision_storage_roots
ok "  System ready"

# --- Step 4: Local stackkit-server image ---------------------------------------

info "Step 4/5 -- Configuring stackkit-server image"
installer_phase prepare-image
configure_stackkit_server_image

# Cloudflare DNS credentials for custom public domains stay environment-driven.
if [ -z "${STACKKIT_DNS_TOKEN:-}" ] && [ -n "${CLOUDFLARE_API_TOKEN:-}" ]; then
  export STACKKIT_DNS_TOKEN="$CLOUDFLARE_API_TOKEN"
fi
if [ -z "${STACKKIT_DNS_EMAIL:-}" ] && [ -n "${CLOUDFLARE_EMAIL:-}" ]; then
  export STACKKIT_DNS_EMAIL="$CLOUDFLARE_EMAIL"
fi

persist_platform_config

# --- Step 5: Generate + Deploy ------------------------------------------------

info "Step 5/5 -- Deploying homelab stack"
installer_phase host-preflight

# Read-only host admission first: naming an unusable device here costs seconds,
# while discovering it during Apply costs a half-mutated host.
#
# This script is served from the website and installs whichever CLI release is
# current, which may predate the admission command. Ask before calling it, so an
# older CLI simply skips the step instead of printing an unknown-command error.
PREFLIGHT_STATUS=0
set +e
if run_stackkit host preflight --help >/dev/null 2>&1; then
  run_stackkit host preflight
  PREFLIGHT_STATUS=$?
fi
set -e

# Undoable host fixes, unattended mode only.
#
# These are the fixes that carry no decision: bounded container logs, and swap
# on a host that has none. Both are reversible, neither needs a reboot or a
# credential, and each keeps a backup of what it replaced. Anything that does
# need one of those stays advice the preflight output already printed.
#
# Set STACKKIT_REMEDIATE=0 to opt out. A failure here never stops the install:
# the finding it targets was a warning, and refusing to install because an
# optional improvement did not take is the failure this admission exists to
# remove, not to cause.
if [ "$INSTALL_MODE" = "auto" ] && [ "${STACKKIT_REMEDIATE:-1}" != "0" ]; then
  set +e
  if run_stackkit host remediate --help >/dev/null 2>&1; then
    STACKKIT_BIN="$(command -v stackkit 2>/dev/null)"
    if [ "$(id -u)" -eq 0 ]; then
      stackkit host remediate --auto-reversible --yes
    elif [ -n "$STACKKIT_BIN" ] && command -v sudo >/dev/null 2>&1; then
      sudo -n "$STACKKIT_BIN" host remediate --auto-reversible --yes
    fi
    run_stackkit host preflight
    PREFLIGHT_STATUS=$?
  fi
  set -e
fi

if [ "$PREFLIGHT_STATUS" -eq 3 ]; then
  echo ""
  err "This host cannot run the StackKit; nothing was applied."
  echo ""
  echo "  To attempt the rollout anyway (it may fail on the same condition):"
  echo "    cd $HOMELAB_DIR && STACKKIT_PREFLIGHT=skip stackkit apply --auto-approve"
  echo ""
  exit 3
fi

# Always resolve the plan for this host as it is now. A plan kept from an
# earlier run predates the host preparation above (Docker, storage roots) or
# the CLI just installed, and Apply rejects it as a binding mismatch. The
# resolution is deterministic, so an unchanged host yields the same plan and
# the apply journal still resumes what already succeeded.
info "Preparing deployment artifacts (stackkit generate)"
installer_phase generate
info "This can take a minute with little extra CLI output"
run_stackkit_step generate
ok "Deployment artifacts are ready"

if [ "${STACKKIT_SKIP_APPLY:-}" = "1" ]; then
  echo ""
  ok "$(t deploy_skip_notice)"
  installer_journal_write state prepared
  echo "  Continue manually:"
  echo "    cd $HOMELAB_DIR"
  echo "    stackkit plan"
  echo "    stackkit apply"
  echo ""
  echo "  Project directory: $HOMELAB_DIR"
  exit 0
fi
info "$(t deploy_notice)"

# One Apply. The canonical ResolvedPlan owns execution order across core
# services and applications, so there is no separate platform-app stage to
# sequence from here; the previous "phase 1/2 then 2/2" pair issued the same
# full Apply twice, which only doubled the exposure to a transient failure.
#
# set -eu would abort this script mid-install on any non-zero exit, leaving the
# operator without a summary or a next step. Capture the status instead and end
# with something actionable.
info "Installing services (stackkit apply)"
installer_phase apply
info "Image download and health waits can take several minutes with little extra output"
if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
  prepare_cloud_install_host || die "Could not prepare this host for Cloud Kit apply. Run the installer again as root on this VPS."
fi
set +e
if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
  _ssh_user=$(execution_channel_username_from_workspace)
  warn "This apply hardens SSH on this host: root login is disabled and password"
  echo "  authentication is turned off. Keep this session open. Afterwards log in as"
  echo "  ${_ssh_user}:  ssh ${_ssh_user}@<this host>"
  _exec_key="$(cloud_execution_channel_key_dir)/id_ed25519"
  if [ -f "$_exec_key" ]; then
    echo "  If this host had no SSH key, use the workspace key:"
    echo "    ssh -i $_exec_key ${_ssh_user}@<this host>"
  fi
  echo ""
fi
# Apply configures the host firewall, sshd and fail2ban, which only root can
# read and change. A non-root run fails there after the port preflight, so hand
# the one Apply to root when passwordless sudo exists, and give the files back.
if [ "$(id -u)" -ne 0 ] && [ "${SELECTED_KIT:-}" != "cloud-kit" ]; then
  _sk_bin="$(command -v stackkit 2>/dev/null)"
  if [ -n "$_sk_bin" ] && command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    info "Applying as root through sudo (host firewall, sshd and fail2ban need it)"
    sudo -n -E env "HOME=$HOME" "PATH=$PATH" "$_sk_bin" apply --auto-approve
    APPLY_STATUS=$?
    sudo -n chown -R "$(id -un):$(id -gn)" "$PWD" >/dev/null 2>&1 || true
  else
    warn "Apply changes host firewall, sshd and fail2ban and needs root."
    echo "  No passwordless sudo found. Re-run the installer as root, or run:"
    echo "    cd $HOMELAB_DIR && sudo -E env HOME=\$HOME stackkit apply --auto-approve"
    run_stackkit apply --auto-approve
    APPLY_STATUS=$?
  fi
else
  run_stackkit apply --auto-approve
  APPLY_STATUS=$?
fi
set -e

if [ "$APPLY_STATUS" -eq 3 ]; then
  echo ""
  err "This host cannot run the StackKit."
  echo ""
  echo "  The findings above are from host admission; nothing was applied."
  echo "  Re-check them at any time:"
  echo "    cd $HOMELAB_DIR && stackkit host preflight"
  echo ""
  echo "  To attempt the rollout anyway (it may fail on the same condition):"
  echo "    cd $HOMELAB_DIR && STACKKIT_PREFLIGHT=skip stackkit apply --auto-approve"
  echo ""
  exit 3
fi

if [ "$APPLY_STATUS" -ne 0 ]; then
  echo ""
  err "The rollout did not complete."
  echo ""
  echo "  What ran, and what failed:"
  echo "    cd $HOMELAB_DIR && stackkit verify --json"
  echo "    cd $HOMELAB_DIR && stackkit logs latest --json"
  echo ""
  if [ "${SELECTED_KIT:-}" = "cloud-kit" ]; then
    echo "  Re-run the installer; it prepares this host and converges the same plan:"
    echo "    curl -sSL https://base.stackkit.cc | sh"
  else
    echo "  Applying again is safe: it converges the same plan and keeps what"
    echo "  already succeeded."
    echo "    cd $HOMELAB_DIR && stackkit apply --auto-approve"
  fi
  echo ""
  echo "  Project directory: $HOMELAB_DIR"
  exit "$APPLY_STATUS"
fi

# Best effort: let this server re-bind itself after it is moved to another
# network. Apply already enables the watcher on root systemd hosts; this covers
# the remaining cases and must never fail the install.
run_stackkit network watch enable >/dev/null 2>&1 || true

# --- Done: print access summary -----------------------------------------------

installer_phase verify
run_stackkit_step verify
installer_complete

DOMAIN_EFFECTIVE="lab.home"
if [ -f "$HOMELAB_DIR/stack-spec.yaml" ]; then
  _d=$(grep -o '"domain":{"base":"[^"]*"' "$HOMELAB_DIR/stack-spec.yaml" | head -1 | sed -E 's/.*"base":"([^"]+)".*/\1/' || true)
  [ -n "$_d" ] && DOMAIN_EFFECTIVE="$_d"
fi

TFVARS="$HOMELAB_DIR/deploy/terraform.tfvars.json"
PAAS=""
ENABLE_HTTPS="false"
ADMIN_PASSWORD=""
# tfvars_value KEY: exact-key extraction that stays correct on single-line JSON.
tfvars_value() {
  grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$TFVARS" 2>/dev/null | head -1 | sed -E 's/.*"([^"]*)"$/\1/' || true
}
if [ -f "$TFVARS" ]; then
  PAAS="coolify" # Legacy tfvars use the historical default.
  _paas=$(tfvars_value paas)
  [ -n "$_paas" ] && PAAS="$_paas"
  if grep -q '"enable_https"[[:space:]]*:[[:space:]]*true' "$TFVARS"; then ENABLE_HTTPS="true"; fi
  ADMIN_PASSWORD=$(tfvars_value admin_password_plaintext)
  _admin_email=$(tfvars_value admin_email)
  [ -n "$_admin_email" ] && ADMIN_EMAIL="$_admin_email"
fi

case "$PAAS" in
  komodo) PAAS_ROUTE="komodo"; PAAS_LABEL="Komodo" ;;
  dokploy) PAAS_ROUTE="dokploy"; PAAS_LABEL="Dokploy" ;;
  coolify) PAAS_ROUTE="coolify"; PAAS_LABEL="Coolify" ;;
  *) PAAS_ROUTE=""; PAAS_LABEL="" ;;
esac

PROTO="http"
if [ "$ENABLE_HTTPS" = "true" ] || [ ! -f "$TFVARS" ]; then PROTO="https"; fi
DASH_URL="${PROTO}://base.${DOMAIN_EFFECTIVE}"
PAAS_URL="${PROTO}://${PAAS_ROUTE}.${DOMAIN_EFFECTIVE}"
AUTH_URL="${PROTO}://auth.${DOMAIN_EFFECTIVE}"
ID_URL="${PROTO}://id.${DOMAIN_EFFECTIVE}"

ACCESS_JSON="$HOMELAB_DIR/.stackkit/access.json"
SETUP_URL=""
CA_FINGERPRINT=""
LINUX_PROFILE_SHA256=""
HUB_URL=""
if [ -f "$ACCESS_JSON" ]; then
  SETUP_URL=$(grep -o '"setupUrl"[[:space:]]*:[[:space:]]*"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)"/\1/' || true)
  CA_FINGERPRINT=$(grep -o '"caFingerprint"[[:space:]]*:[[:space:]]*"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)"/\1/' || true)
  LINUX_PROFILE_SHA256=$(grep -o '"linuxProfileSha256"[[:space:]]*:[[:space:]]*"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)"/\1/' || true)
  HUB_URL=$(grep -o '"hubUrl"[[:space:]]*:[[:space:]]*"[^"]*"' "$ACCESS_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)"/\1/' || true)
fi

SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_SERVER_IP")

echo ""
ok "Your homelab is running!"
echo ""
printf '\033[38;5;208m'
if [ -n "$SETUP_URL" ]; then
  echo "  Second device (same home network): $SETUP_URL"
  echo "    Open this setup page first; it needs no DNS or certificate setup."
  echo "    Choose your phone or laptop, configure DNS, then approve the homelab CA."
  echo "    Before approving trust, compare this Owner-CA fingerprint with the page:"
  echo "    $CA_FINGERPRINT"
  echo "    Linux adapter SHA-256 (compare the downloaded file before running sudo):"
  echo "    $LINUX_PROFILE_SHA256"
  echo "    Then open the HTTPS Hub and create your PocketID passkey to sign in."
  echo ""
fi
echo "  Dashboard:  ${HUB_URL:-$DASH_URL}"
printf '\033[0m'
echo ""
echo "  Core services:"
echo "    ${HUB_URL:-$DASH_URL}    Base hub"
if [ -n "$PAAS_ROUTE" ]; then
  echo "    ${PAAS_URL}    ${PAAS_LABEL} controller"
fi
echo "    ${AUTH_URL}    Authentication"
echo "    ${ID_URL}    Identity (PocketID)"
echo ""
echo "  Initial admin login credentials:"
if [ -f "$TFVARS" ]; then
  echo "    Use for: TinyAuth gateway and ${PAAS_LABEL} initial admin"
  echo "    Email:    ${ADMIN_EMAIL:-generated (see $TFVARS)}"
  if [ -n "$ADMIN_PASSWORD" ]; then
    echo "    Password: ${ADMIN_PASSWORD}"
  fi
else
  # Native v2: TinyAuth federates to PocketID; there is no shared admin
  # password. The owner identity is a passkey created at first login.
  echo "    Identity: PocketID passkey (no shared admin password on native v2)"
  echo "    Create it at ${ID_URL}/setup, then sign in at ${AUTH_URL}"
  if [ -n "${ADMIN_EMAIL:-}" ]; then
    echo "    Owner email: ${ADMIN_EMAIL}"
  fi
  if [ -n "${OWNER_USERNAME:-}" ]; then
    echo "    Login username: ${OWNER_USERNAME}"
  fi
fi
echo ""
echo "  Next steps:"
if [ "${BOOTSTRAP_OWNER:-}" = "true" ]; then
  echo "    1. Complete the one-time PocketID owner setup URL printed above"
else
  echo "    1. Create your PocketID admin passkey at ${ID_URL}/setup"
fi
echo "    2. Sign in at ${AUTH_URL}"
echo "    3. Open ${HUB_URL:-$DASH_URL} and protect the hub after owner setup"
echo ""
if [ -z "$SETUP_URL" ]; then
  case "$DOMAIN_EFFECTIVE" in
    localhost|*.localhost)
      echo "  These .localhost links open only on the server. Choose a LAN domain to use another device."
      ;;
    *.local|*.lab|*.lan|*.home|*.internal|*.test|home|homelab)
      echo "  Second-device setup is unavailable; do not try the private names yet."
      echo "  On the server, repair it with: stackkit -C \"$HOMELAB_DIR\" verify client-trust --export"
      ;;
    *)
      echo "  Second device: open ${HUB_URL:-$DASH_URL}; public DNS and HTTPS need no client setup."
      echo "  If it does not open, check the selected public reach mode and DNS on the server."
      ;;
  esac
  echo ""
fi
echo "  Commands:"
echo "    stackkit logs latest --json  Inspect local rollout evidence"
echo "    Moving this server to another network? Run: stackkit network wifi add --ssid <name> --password-stdin"
echo "    before the move; it re-binds itself after the move."
echo "    cat $ACCESS_JSON             Read service URLs and setup evidence"
echo ""
echo "  Uninstall: deletes ALL StackKits projects and application volumes on this host."
echo "    Back up needed data first. This is not limited to the project directory."
echo "    Review the scope, then confirm interactively:"
printf "    (cd -- '"
printf '%s' "$HOMELAB_DIR" | sed "s/'/'\\\\''/g"
printf "' && stackkit remove)\n"
echo "    Only after explicitly consenting to that deletion, run without a prompt:"
printf "    (cd -- '"
printf '%s' "$HOMELAB_DIR" | sed "s/'/'\\\\''/g"
printf "' && stackkit remove --auto-approve)\n"
echo ""
if [ -f "$ACCESS_JSON" ]; then
  echo "  Machine-readable access summary:"
  echo "    $ACCESS_JSON"
  echo ""
fi
echo "  Project directory: $HOMELAB_DIR"
echo ""
