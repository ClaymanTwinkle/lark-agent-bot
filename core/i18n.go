package core

import (
	"fmt"
	"strings"
	"sync"
)

// Language represents a supported language
type Language string

const (
	LangAuto               Language = "" // auto-detect from user messages
	LangEnglish            Language = "en"
	LangChinese            Language = "zh"
	LangTraditionalChinese Language = "zh-TW"
	LangJapanese           Language = "ja"
	LangSpanish            Language = "es"
)

// I18n provides internationalized messages.
//
// All exported methods are safe to call from multiple goroutines: lark-agent-bot
// fans out platform message handlers concurrently, all of which can call
// DetectAndSet (writes `detected`) and T / CurrentLang (read `lang`/`detected`)
// at the same time. Without the mutex `go test -race` flags real data races
// on the language fields.
type I18n struct {
	mu       sync.RWMutex
	lang     Language
	detected Language
	saveFunc func(Language) error
}

func NewI18n(lang Language) *I18n {
	return &I18n{lang: lang}
}

func (i *I18n) SetSaveFunc(fn func(Language) error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.saveFunc = fn
}

func DetectLanguage(text string) Language {
	for _, r := range text {
		if isJapanese(r) {
			return LangJapanese
		}
	}
	for _, r := range text {
		if isChinese(r) {
			return LangChinese
		}
	}
	if isSpanishHint(text) {
		return LangSpanish
	}
	return LangEnglish
}

// NormalizeLanguageString parses a configuration string (e.g. from
// opts["language"] or [projects].language) into a Language constant. The
// accepted spellings mirror the ones documented in config.example.toml:
// "en"/"english", "zh"/"chinese", "zh-TW"/"zh_TW"/"zhtw", "ja"/"japanese",
// "es"/"spanish", and "" / "auto" for auto-detect. Unknown values return
// LangAuto so the engine falls back to detection rather than silently
// snapping to English — operators who set language = "klingon" should see
// "auto" behaviour, not a hard English default they didn't ask for.
//
// Issue cc-connect#1655 introduced this helper so the Claude Code agent (which
// receives the language via opts["language"]) can decode the string
// without duplicating the switch statement that cmd/lark-agent-bot/main.go
// uses for engine construction.
func NormalizeLanguageString(s string) Language {
	switch strings.ToLower(s) {
	case "en", "english":
		return LangEnglish
	case "zh", "chinese":
		return LangChinese
	case "zh-tw", "zh_tw", "zhtw":
		return LangTraditionalChinese
	case "ja", "japanese":
		return LangJapanese
	case "es", "spanish":
		return LangSpanish
	case "", "auto":
		return LangAuto
	default:
		return LangAuto
	}
}

func isChinese(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

func isJapanese(r rune) bool {
	return (r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0x31F0 && r <= 0x31FF) || // Katakana Phonetic Extensions
		(r >= 0xFF65 && r <= 0xFF9F) // Half-width Katakana
}

// isSpanishHint checks for characters common in Spanish but not English (ñ, ¿, ¡, accented vowels).
func isSpanishHint(text string) bool {
	for _, r := range text {
		switch r {
		case 'ñ', 'Ñ', '¿', '¡', 'á', 'é', 'í', 'ó', 'ú', 'ü':
			return true
		}
	}
	return false
}

func (i *I18n) DetectAndSet(text string) {
	i.mu.RLock()
	if i.lang != LangAuto {
		i.mu.RUnlock()
		return
	}
	currentDetected := i.detected
	i.mu.RUnlock()

	detected := DetectLanguage(text)
	if currentDetected == detected {
		return
	}

	i.mu.Lock()
	// Re-check under the write lock — another goroutine may have updated
	// i.detected between our RUnlock above and the Lock here.
	if i.lang != LangAuto || i.detected == detected {
		i.mu.Unlock()
		return
	}
	i.detected = detected
	saveFunc := i.saveFunc
	i.mu.Unlock()

	if saveFunc != nil {
		if err := saveFunc(detected); err != nil {
			fmt.Printf("failed to save language: %v\n", err)
		}
	}
}

func (i *I18n) currentLang() Language {
	// Caller holds either no lock (most public methods take RLock and call
	// this) or RLock; this helper just reads the protected fields. All
	// public methods that call currentLang() acquire i.mu.RLock first.
	if i.lang == LangAuto {
		if i.detected != "" {
			return i.detected
		}
		return LangEnglish
	}
	return i.lang
}

// CurrentLang returns the resolved language (exported for mode display).
func (i *I18n) CurrentLang() Language {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.currentLang()
}

// IsZhLike returns true for Simplified and Traditional Chinese.
func (i *I18n) IsZhLike() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	l := i.currentLang()
	return l == LangChinese || l == LangTraditionalChinese
}

// SetLang overrides the language (disabling auto-detect).
func (i *I18n) SetLang(lang Language) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.lang = lang
	i.detected = ""
}

// Message keys
type MsgKey string

// Setup workflow messages are shared by CLI frontends.
const (
	MsgSetupPrepared             MsgKey = "setup_prepared"
	MsgSetupIncomplete           MsgKey = "setup_incomplete"
	MsgSetupMissing              MsgKey = "setup_missing"
	MsgSetupChecked              MsgKey = "setup_checked"
	MsgSetupSubscriptionsUnknown MsgKey = "setup_subscriptionsunknown"
	MsgSetupOwnerUnknown         MsgKey = "setup_ownerunknown"
	MsgSetupTargetOccupied       MsgKey = "setup_targetoccupied"
	MsgSetupMenuNotice           MsgKey = "setup_menunotice"
	MsgSetupMenuGuidance         MsgKey = "setup_menuguidance"
	MsgSetupProjectCreated       MsgKey = "setup_projectcreated"
	MsgSetupStarterFilled        MsgKey = "setup_starterfilled"
	MsgSetupPlatformAdded        MsgKey = "setup_platformadded"
	MsgSetupWorkDirFilled        MsgKey = "setup_workdirfilled"
	MsgSetupConfigCreated        MsgKey = "setup_configcreated"
	MsgSetupNoProjects           MsgKey = "setup_noprojects"
)

// Messages of the `lark-agent-bot feishu`, `doctor` and `web` commands.
const (
	MsgSetupScanQR                     MsgKey = "setup_scan_qr"
	MsgCLIWebNotBuilt                  MsgKey = "cli_web_not_built"
	MsgCLIWebConfigCreated             MsgKey = "cli_web_config_created"
	MsgCLIWebEnabling                  MsgKey = "cli_web_enabling"
	MsgCLIWebEnabled                   MsgKey = "cli_web_enabled"
	MsgCLIWebOpening                   MsgKey = "cli_web_opening"
	MsgCLIWebOpenFailed                MsgKey = "cli_web_open_failed"
	MsgCLIWebStarting                  MsgKey = "cli_web_starting"
	MsgCLIWebManualStart               MsgKey = "cli_web_manual_start"
	MsgCLIWebRunningNeedsRestart       MsgKey = "cli_web_running_needs_restart"
	MsgCLIWebUnavailable               MsgKey = "cli_web_unavailable"
	MsgCLIDoctorConfigMissing          MsgKey = "cli_doctor_config_missing"
	MsgCLIDoctorConfigInvalid          MsgKey = "cli_doctor_config_invalid"
	MsgCLIDoctorConfigOK               MsgKey = "cli_doctor_config_ok"
	MsgCLIDoctorNoProjects             MsgKey = "cli_doctor_no_projects"
	MsgCLIDoctorProjects               MsgKey = "cli_doctor_projects"
	MsgCLIDoctorWorkDirOK              MsgKey = "cli_doctor_work_dir_ok"
	MsgCLIDoctorWorkDirUnset           MsgKey = "cli_doctor_work_dir_unset"
	MsgCLIDoctorWorkDirPlaceholder     MsgKey = "cli_doctor_work_dir_placeholder"
	MsgCLIDoctorWorkDirMissing         MsgKey = "cli_doctor_work_dir_missing"
	MsgCLIDoctorAgentOK                MsgKey = "cli_doctor_agent_ok"
	MsgCLIDoctorAgentUnknown           MsgKey = "cli_doctor_agent_unknown"
	MsgCLIDoctorAgentFailed            MsgKey = "cli_doctor_agent_failed"
	MsgCLIDoctorAgentCLIOK             MsgKey = "cli_doctor_agent_cli_ok"
	MsgCLIDoctorAgentCLIMissing        MsgKey = "cli_doctor_agent_cli_missing"
	MsgCLIDoctorAgentCLIFound          MsgKey = "cli_doctor_agent_cli_found"
	MsgCLIDoctorAgentCLIRunAs          MsgKey = "cli_doctor_agent_cli_run_as"
	MsgCLIDoctorPlatformUnknown        MsgKey = "cli_doctor_platform_unknown"
	MsgCLIDoctorCredentialsEmpty       MsgKey = "cli_doctor_credentials_empty"
	MsgCLIDoctorCredentialsPlaceholder MsgKey = "cli_doctor_credentials_placeholder"
	MsgCLIDoctorCredentialsOK          MsgKey = "cli_doctor_credentials_ok"
)

const (
	// Permission presets and validation.
	MsgModeInvalid                MsgKey = "mode_invalid"
	MsgPermissionDefaultName      MsgKey = "permission_default_name"
	MsgPermissionDefaultDesc      MsgKey = "permission_default_desc"
	MsgPermissionAutoReviewName   MsgKey = "permission_auto_review_name"
	MsgPermissionAutoReviewDesc   MsgKey = "permission_auto_review_desc"
	MsgPermissionReadOnlyName     MsgKey = "permission_read_only_name"
	MsgPermissionReadOnlyDesc     MsgKey = "permission_read_only_desc"
	MsgPermissionReadOnlyExecDesc MsgKey = "permission_read_only_exec_desc"
	MsgPermissionFullAccessName   MsgKey = "permission_full_access_name"
	MsgPermissionFullAccessDesc   MsgKey = "permission_full_access_desc"

	MsgStarting                  MsgKey = "starting"
	MsgThinking                  MsgKey = "thinking"
	MsgTool                      MsgKey = "tool"
	MsgToolResult                MsgKey = "tool_result"
	MsgToolResultFmtStatus       MsgKey = "tool_result_fmt_status"
	MsgToolResultFmtExit         MsgKey = "tool_result_fmt_exit"
	MsgToolResultFmtNoOutput     MsgKey = "tool_result_fmt_no_output"
	MsgToolResultFmtOk           MsgKey = "tool_result_fmt_ok"
	MsgToolResultFmtFailed       MsgKey = "tool_result_fmt_failed"
	MsgExecutionStopped          MsgKey = "execution_stopped"
	MsgSessionCloseFailed        MsgKey = "session_close_failed"
	MsgSessionResumeUnsafe       MsgKey = "session_resume_unsafe"
	MsgSessionCancelled          MsgKey = "session_cancelled"
	MsgNoExecution               MsgKey = "no_execution"
	MsgPreviousProcessing        MsgKey = "previous_processing"
	MsgQueueFull                 MsgKey = "queue_full"
	MsgMessageQueued             MsgKey = "message_queued"
	MsgRecallQueuedCancelled     MsgKey = "recall_queued_cancelled"
	MsgRecallActiveStopping      MsgKey = "recall_active_stopping"
	MsgRecallQueuedDropped       MsgKey = "recall_queued_dropped"
	MsgNoToolsAllowed            MsgKey = "no_tools_allowed"
	MsgCurrentTools              MsgKey = "current_tools"
	MsgCurrentSession            MsgKey = "current_session"
	MsgToolAuthNotSupported      MsgKey = "tool_auth_not_supported"
	MsgToolAllowFailed           MsgKey = "tool_allow_failed"
	MsgToolAllowedNew            MsgKey = "tool_allowed_new"
	MsgError                     MsgKey = "error"
	MsgSessionNotFound           MsgKey = "session_not_found"
	MsgFailedToStartAgentSession MsgKey = "failed_to_start_agent_session"
	MsgFailedToDeleteSession     MsgKey = "failed_to_delete_session"
	MsgEmptyResponse             MsgKey = "empty_response"
	MsgPermissionPrompt          MsgKey = "permission_prompt"
	MsgPermissionAllowed         MsgKey = "permission_allowed"
	MsgPermissionApproveAll      MsgKey = "permission_approve_all"
	MsgPermissionDenied          MsgKey = "permission_denied_msg"
	MsgPermissionHint            MsgKey = "permission_hint"
	MsgPermissionNotRequester    MsgKey = "permission_not_requester"
	MsgQuietOn                   MsgKey = "quiet_on"
	MsgQuietOff                  MsgKey = "quiet_off"
	MsgDisplayModeCompact        MsgKey = "display_mode_compact"
	MsgQuietGlobalOn             MsgKey = "quiet_global_on"
	MsgQuietGlobalOff            MsgKey = "quiet_global_off"
	MsgModeChanged               MsgKey = "mode_changed"
	MsgModeNotSupported          MsgKey = "mode_not_supported"
	MsgSessionRestarting         MsgKey = "session_restarting"
	MsgSessionNotStarted         MsgKey = "session_not_started"
	MsgUntitled                  MsgKey = "untitled"
	MsgLangChanged               MsgKey = "lang_changed"
	MsgLangInvalid               MsgKey = "lang_invalid"
	MsgLangCurrent               MsgKey = "lang_current"
	MsgUnknownCommand            MsgKey = "unknown_command"
	MsgWelcome                   MsgKey = "welcome"
	MsgHelp                      MsgKey = "message_help" // change from "help", which is used now for builtin command help
	MsgHelpTitle                 MsgKey = "help_title"
	MsgHelpSessionSection        MsgKey = "help_session_section"
	MsgHelpAgentSection          MsgKey = "help_agent_section"
	MsgHelpToolsSection          MsgKey = "help_tools_section"
	MsgHelpSystemSection         MsgKey = "help_system_section"
	MsgHelpTip                   MsgKey = "help_tip"
	MsgListTitle                 MsgKey = "list_title"
	MsgListTitlePaged            MsgKey = "list_title_paged"
	MsgListEmpty                 MsgKey = "list_empty"
	MsgListMore                  MsgKey = "list_more"
	MsgListPageHint              MsgKey = "list_page_hint"
	MsgListSwitchHint            MsgKey = "list_switch_hint"
	MsgListError                 MsgKey = "list_error"
	MsgHistoryEmpty              MsgKey = "history_empty"
	MsgNameUsage                 MsgKey = "name_usage"
	MsgNameSet                   MsgKey = "name_set"
	MsgNameNoSession             MsgKey = "name_no_session"
	MsgProviderNotSupported      MsgKey = "provider_not_supported"
	MsgProviderNone              MsgKey = "provider_none"
	MsgProviderCurrent           MsgKey = "provider_current"
	MsgProviderListTitle         MsgKey = "provider_list_title"
	MsgProviderListEmpty         MsgKey = "provider_list_empty"
	MsgProviderSwitchHint        MsgKey = "provider_switch_hint"
	MsgProviderNotFound          MsgKey = "provider_not_found"
	MsgProviderSwitched          MsgKey = "provider_switched"
	MsgProviderCleared           MsgKey = "provider_cleared"
	MsgProviderAdded             MsgKey = "provider_added"
	MsgProviderAddUsage          MsgKey = "provider_add_usage"
	MsgProviderAddFailed         MsgKey = "provider_add_failed"
	MsgProviderRemoved           MsgKey = "provider_removed"
	MsgProviderRemoveFailed      MsgKey = "provider_remove_failed"
	MsgCardTitleProviderAdd      MsgKey = "card_title_provider_add"
	MsgProviderAddPickHint       MsgKey = "provider_add_pick_hint"
	MsgProviderAddOther          MsgKey = "provider_add_other"
	MsgProviderAddApiKeyPrompt   MsgKey = "provider_add_api_key_prompt"
	MsgProviderAddInviteHint     MsgKey = "provider_add_invite_hint"
	MsgProviderLinkGlobal        MsgKey = "provider_link_global"
	MsgProviderLinked            MsgKey = "provider_linked"

	MsgVoiceNotEnabled               MsgKey = "voice_not_enabled"
	MsgVoiceUsingPlatformRecognition MsgKey = "voice_using_platform_recognition"
	MsgVoiceNoFFmpeg                 MsgKey = "voice_no_ffmpeg"
	MsgVoiceTranscribing             MsgKey = "voice_transcribing"
	MsgVoiceTranscribed              MsgKey = "voice_transcribed"
	MsgVoiceTranscribeFailed         MsgKey = "voice_transcribe_failed"
	MsgVoiceEmpty                    MsgKey = "voice_empty"

	MsgTTSNotEnabled MsgKey = "tts_not_enabled"
	MsgTTSStatus     MsgKey = "tts_status"
	MsgTTSSwitched   MsgKey = "tts_switched"
	MsgTTSUsage      MsgKey = "tts_usage"

	MsgHeartbeatNotAvailable MsgKey = "heartbeat_not_available"
	MsgHeartbeatStatus       MsgKey = "heartbeat_status"
	MsgHeartbeatPaused       MsgKey = "heartbeat_paused"
	MsgHeartbeatResumed      MsgKey = "heartbeat_resumed"
	MsgHeartbeatInterval     MsgKey = "heartbeat_interval"
	MsgHeartbeatTriggered    MsgKey = "heartbeat_triggered"
	MsgHeartbeatUsage        MsgKey = "heartbeat_usage"
	MsgHeartbeatInvalidMins  MsgKey = "heartbeat_invalid_mins"

	MsgCronNotAvailable       MsgKey = "cron_not_available"
	MsgCronUsage              MsgKey = "cron_usage"
	MsgCronAddUsage           MsgKey = "cron_add_usage"
	MsgCronAdded              MsgKey = "cron_added"
	MsgCronAddedExec          MsgKey = "cron_added_exec"
	MsgCronAddExecUsage       MsgKey = "cron_addexec_usage"
	MsgCronEmpty              MsgKey = "cron_empty"
	MsgCronListTitle          MsgKey = "cron_list_title"
	MsgCronListFooter         MsgKey = "cron_list_footer"
	MsgCronExecUsage          MsgKey = "cron_exec_usage"
	MsgCronTriggered          MsgKey = "cron_triggered"
	MsgCronProjectUnavailable MsgKey = "cron_project_unavailable"
	MsgCronDelUsage           MsgKey = "cron_del_usage"
	MsgCronDeleted            MsgKey = "cron_deleted"
	MsgCronNotFound           MsgKey = "cron_not_found"
	MsgCronEnabled            MsgKey = "cron_enabled"
	MsgCronDisabled           MsgKey = "cron_disabled"
	MsgCronMuted              MsgKey = "cron_muted"
	MsgCronUnmuted            MsgKey = "cron_unmuted"
	MsgCronCardHint           MsgKey = "cron_card_hint"
	MsgCronNextShort          MsgKey = "cron_next_short"
	MsgCronLastShort          MsgKey = "cron_last_short"
	MsgCronBtnEnable          MsgKey = "cron_btn_enable"
	MsgCronBtnDisable         MsgKey = "cron_btn_disable"
	MsgCronBtnMute            MsgKey = "cron_btn_mute"
	MsgCronBtnUnmute          MsgKey = "cron_btn_unmute"
	MsgCronBtnDelete          MsgKey = "cron_btn_delete"

	MsgStatusTitle           MsgKey = "status_title"
	MsgReplyFooterRemaining  MsgKey = "reply_footer_remaining"
	MsgReplyFooterQuota5h    MsgKey = "reply_footer_quota_5h"
	MsgReplyFooterQuotaWeek  MsgKey = "reply_footer_quota_week"
	MsgModelCurrent          MsgKey = "model_current"
	MsgModelChanged          MsgKey = "model_changed"
	MsgModelChangeFailed     MsgKey = "model_change_failed"
	MsgModelCardSwitching    MsgKey = "model_card_switching"
	MsgModelCardSwitched     MsgKey = "model_card_switched"
	MsgModelCardSwitchFailed MsgKey = "model_card_switch_failed"
	MsgModelNotSupported     MsgKey = "model_not_supported"
	MsgReasoningCurrent      MsgKey = "reasoning_current"
	MsgReasoningChanged      MsgKey = "reasoning_changed"
	MsgReasoningNotSupported MsgKey = "reasoning_not_supported"

	MsgTurnStoppedBySettingChange MsgKey = "turn_stopped_by_setting_change"
	MsgResumeFailedNewSession     MsgKey = "resume_failed_new_session"

	MsgCompressNotSupported MsgKey = "compress_not_supported"
	MsgCompressing          MsgKey = "compressing"
	MsgCompressNoSession    MsgKey = "compress_no_session"
	MsgCompressDone         MsgKey = "compress_done"

	MsgMemoryNotSupported MsgKey = "memory_not_supported"
	MsgMemoryShowProject  MsgKey = "memory_show_project"
	MsgMemoryShowGlobal   MsgKey = "memory_show_global"
	MsgMemoryEmpty        MsgKey = "memory_empty"
	MsgMemoryAdded        MsgKey = "memory_added"
	MsgMemoryAddFailed    MsgKey = "memory_add_failed"
	MsgMemoryAddUsage     MsgKey = "memory_add_usage"
	MsgUsageNotSupported  MsgKey = "usage_not_supported"
	MsgUsageFetchFailed   MsgKey = "usage_fetch_failed"

	// Inline strings previously hardcoded in engine.go
	MsgStatusMode             MsgKey = "status_mode"
	MsgStatusSession          MsgKey = "status_session"
	MsgStatusCron             MsgKey = "status_cron"
	MsgStatusThinkingMessages MsgKey = "status_thinking_messages"
	MsgStatusToolMessages     MsgKey = "status_tool_messages"
	MsgStatusSessionKey       MsgKey = "status_session_key"
	MsgStatusAgentSID         MsgKey = "status_agent_sid"
	MsgStatusUserID           MsgKey = "status_user_id"
	MsgEnabledShort           MsgKey = "enabled_short"
	MsgDisabledShort          MsgKey = "disabled_short"

	MsgModelDefault               MsgKey = "model_default"
	MsgModelListTitle             MsgKey = "model_list_title"
	MsgModelUsage                 MsgKey = "model_usage"
	MsgReasoningDefault           MsgKey = "reasoning_default"
	MsgReasoningListTitle         MsgKey = "reasoning_list_title"
	MsgReasoningUsage             MsgKey = "reasoning_usage"
	MsgReasoningSelectPlaceholder MsgKey = "reasoning_select_placeholder"

	MsgModeUsage                 MsgKey = "mode_usage"
	MsgLangSelectPlaceholder     MsgKey = "lang_select_placeholder"
	MsgModelSelectPlaceholder    MsgKey = "model_select_placeholder"
	MsgModeSelectPlaceholder     MsgKey = "mode_select_placeholder"
	MsgProviderSelectPlaceholder MsgKey = "provider_select_placeholder"
	MsgProviderClearOption       MsgKey = "provider_clear_option"
	MsgCardBack                  MsgKey = "card_back"
	MsgCardPrev                  MsgKey = "card_prev"
	MsgCardNext                  MsgKey = "card_next"
	MsgCardTitleStatus           MsgKey = "card_title_status"
	MsgCardTitleLanguage         MsgKey = "card_title_language"
	MsgCardTitleModel            MsgKey = "card_title_model"
	MsgCardTitleReasoning        MsgKey = "card_title_reasoning"
	MsgCardTitleMode             MsgKey = "card_title_mode"
	MsgCardTitleSessions         MsgKey = "card_title_sessions"
	MsgCardTitleSessionsPaged    MsgKey = "card_title_sessions_paged"
	MsgCardTitleCurrentSession   MsgKey = "card_title_current_session"
	MsgCardTitleHistory          MsgKey = "card_title_history"
	MsgCardTitleHistoryLast      MsgKey = "card_title_history_last"
	MsgCardTitleProvider         MsgKey = "card_title_provider"
	MsgCardTitleCron             MsgKey = "card_title_cron"
	MsgCardTitleTimer            MsgKey = "card_title_timer"
	MsgCardTitleHeartbeat        MsgKey = "card_title_heartbeat"
	MsgCardTitleCommands         MsgKey = "card_title_commands"
	MsgCardTitleAlias            MsgKey = "card_title_alias"
	MsgCardTitleConfig           MsgKey = "card_title_config"
	MsgCardTitleSkills           MsgKey = "card_title_skills"
	MsgCardTitleDoctor           MsgKey = "card_title_doctor"
	MsgCardTitleVersion          MsgKey = "card_title_version"
	MsgCardTitleUpgrade          MsgKey = "card_title_upgrade"
	MsgListItem                  MsgKey = "list_item"
	MsgListEmptySummary          MsgKey = "list_empty_summary"
	MsgCronIDLabel               MsgKey = "cron_id_label"
	MsgCronFailedSuffix          MsgKey = "cron_failed_suffix"

	MsgTimerNotAvailable    MsgKey = "timer_not_available"
	MsgTimerUsage           MsgKey = "timer_usage"
	MsgTimerAddUsage        MsgKey = "timer_add_usage"
	MsgTimerAdded           MsgKey = "timer_added"
	MsgTimerAddedExec       MsgKey = "timer_added_exec"
	MsgTimerAddExecUsage    MsgKey = "timer_addexec_usage"
	MsgTimerEmpty           MsgKey = "timer_empty"
	MsgTimerListTitle       MsgKey = "timer_list_title"
	MsgTimerListFooter      MsgKey = "timer_list_footer"
	MsgTimerDelUsage        MsgKey = "timer_del_usage"
	MsgTimerMuteUsage       MsgKey = "timer_mute_usage"
	MsgTimerDeleted         MsgKey = "timer_deleted"
	MsgTimerNotFound        MsgKey = "timer_not_found"
	MsgTimerMuted           MsgKey = "timer_muted"
	MsgTimerUnmuted         MsgKey = "timer_unmuted"
	MsgTimerCardHint        MsgKey = "timer_card_hint"
	MsgTimerBtnMute         MsgKey = "timer_btn_mute"
	MsgTimerBtnUnmute       MsgKey = "timer_btn_unmute"
	MsgTimerBtnDelete       MsgKey = "timer_btn_delete"
	MsgTimerIDLabel         MsgKey = "timer_id_label"
	MsgTimerScheduledLabel  MsgKey = "timer_scheduled_label"
	MsgTimerFailedSuffix    MsgKey = "timer_failed_suffix"
	MsgCommandsTagAgent     MsgKey = "commands_tag_agent"
	MsgCommandsTagShell     MsgKey = "commands_tag_shell"
	MsgUpgradeTimeoutSuffix MsgKey = "upgrade_timeout_suffix"

	MsgCronScheduleLabel MsgKey = "cron_schedule_label"
	MsgCronNextRunLabel  MsgKey = "cron_next_run_label"
	MsgCronLastRunLabel  MsgKey = "cron_last_run_label"

	MsgPermBtnAllow    MsgKey = "perm_btn_allow"
	MsgPermBtnDeny     MsgKey = "perm_btn_deny"
	MsgPermBtnAllowAll MsgKey = "perm_btn_allow_all"
	MsgPermCardTitle   MsgKey = "perm_card_title"
	MsgPermCardBody    MsgKey = "perm_card_body"
	MsgPermCardNote    MsgKey = "perm_card_note"

	MsgAskQuestionTitle     MsgKey = "ask_question_title"
	MsgAskQuestionNote      MsgKey = "ask_question_note"
	MsgAskQuestionNoteMulti MsgKey = "ask_question_note_multi"
	MsgAskQuestionMulti     MsgKey = "ask_question_multi"
	MsgAskQuestionPrompt    MsgKey = "ask_question_prompt"
	MsgAskQuestionAnswered  MsgKey = "ask_question_answered"

	MsgCommandsTitle        MsgKey = "commands_title"
	MsgCommandsEmpty        MsgKey = "commands_empty"
	MsgCommandsHint         MsgKey = "commands_hint"
	MsgCommandsUsage        MsgKey = "commands_usage"
	MsgCommandsAddUsage     MsgKey = "commands_add_usage"
	MsgCommandsAddExecUsage MsgKey = "commands_addexec_usage"
	MsgCommandsAdded        MsgKey = "commands_added"
	MsgCommandsExecAdded    MsgKey = "commands_exec_added"
	MsgCommandsAddExists    MsgKey = "commands_add_exists"
	MsgCommandsDelUsage     MsgKey = "commands_del_usage"
	MsgCommandsDeleted      MsgKey = "commands_deleted"
	MsgCommandsNotFound     MsgKey = "commands_not_found"

	MsgCommandExecTimeout MsgKey = "command_exec_timeout"
	MsgCommandExecError   MsgKey = "command_exec_error"
	MsgCommandExecSuccess MsgKey = "command_exec_success"

	MsgSkillsTitle MsgKey = "skills_title"
	MsgSkillsEmpty MsgKey = "skills_empty"
	MsgSkillsHint  MsgKey = "skills_hint"

	MsgConfigTitle       MsgKey = "config_title"
	MsgConfigHint        MsgKey = "config_hint"
	MsgConfigGetUsage    MsgKey = "config_get_usage"
	MsgConfigSetUsage    MsgKey = "config_set_usage"
	MsgConfigUpdated     MsgKey = "config_updated"
	MsgConfigKeyNotFound MsgKey = "config_key_not_found"
	MsgConfigReloaded    MsgKey = "config_reloaded"

	MsgDoctorRunning MsgKey = "doctor_running"
	MsgDoctorTitle   MsgKey = "doctor_title"
	MsgDoctorSummary MsgKey = "doctor_summary"

	MsgRestarting     MsgKey = "restarting"
	MsgRestartSuccess MsgKey = "restart_success"

	MsgAgentExitedMidTurn MsgKey = "agent_exited_mid_turn"
	MsgTurnInterrupted    MsgKey = "turn_interrupted"
	MsgTimerInterrupted   MsgKey = "timer_interrupted"
	MsgStallModel         MsgKey = "stall_model"
	MsgStallTool          MsgKey = "stall_tool"

	MsgRetryNotice           MsgKey = "retry_notice"
	MsgRetryNextIn           MsgKey = "retry_next_in"
	MsgRetryReasonRateLimit  MsgKey = "retry_reason_rate_limit"
	MsgRetryReasonOverloaded MsgKey = "retry_reason_overloaded"
	MsgRetryReasonAuth       MsgKey = "retry_reason_auth"
	MsgRetryReasonServer     MsgKey = "retry_reason_server"
	MsgRetryReasonNoResponse MsgKey = "retry_reason_no_response"
	MsgRetryReasonNetwork    MsgKey = "retry_reason_network"

	MsgUpgradeChecking    MsgKey = "upgrade_checking"
	MsgUpgradeUpToDate    MsgKey = "upgrade_up_to_date"
	MsgUpgradeAvailable   MsgKey = "upgrade_available"
	MsgUpgradeDownloading MsgKey = "upgrade_downloading"
	MsgUpgradeSuccess     MsgKey = "upgrade_success"
	MsgUpgradeDevBuild    MsgKey = "upgrade_dev_build"
	// MsgUpgradeAlreadyInstalled: the binary on disk is already the new
	// version (another bot sharing it upgraded), so only a restart is needed.
	MsgUpgradeAlreadyInstalled MsgKey = "upgrade_already_installed"
	// MsgUpgradeRestartWaiting: the update is installed, and the restart
	// waits for the tasks in progress to finish.
	MsgUpgradeRestartWaiting MsgKey = "upgrade_restart_waiting"
	// MsgPeerVersionMismatch: other lark-agent-bot processes on this machine
	// run another version, which breaks relays between the bots. Args: this
	// bot's version, then the other bots with their versions.
	MsgPeerVersionMismatch MsgKey = "peer_version_mismatch"
	// MsgUpgradeConfirmButton labels the upgrade card button that runs
	// /upgrade confirm.
	MsgUpgradeConfirmButton MsgKey = "upgrade_confirm_button"

	MsgWebNotSupported MsgKey = "web_not_supported"
	MsgWebNotEnabled   MsgKey = "web_not_enabled"
	MsgWebSetupSuccess MsgKey = "web_setup_success"
	MsgWebNeedRestart  MsgKey = "web_need_restart"
	MsgWebStatus       MsgKey = "web_status"

	MsgAliasEmpty      MsgKey = "alias_empty"
	MsgAliasListHeader MsgKey = "alias_list_header"
	MsgAliasAdded      MsgKey = "alias_added"
	MsgAliasDeleted    MsgKey = "alias_deleted"
	MsgAliasNotFound   MsgKey = "alias_not_found"
	MsgAliasUsage      MsgKey = "alias_usage"

	MsgNewSessionCreated      MsgKey = "new_session_created"
	MsgNewSessionCreatedName  MsgKey = "new_session_created_name"
	MsgSessionAutoResetIdle   MsgKey = "session_auto_reset_idle"
	MsgSessionClosingGraceful MsgKey = "session_closing_graceful"

	MsgDeleteUsage              MsgKey = "delete_usage"
	MsgDeleteSuccess            MsgKey = "delete_success"
	MsgDeleteActiveDenied       MsgKey = "delete_active_denied"
	MsgDeleteNotSupported       MsgKey = "delete_not_supported"
	MsgDeleteModeTitle          MsgKey = "delete_mode_title"
	MsgDeleteModeSelect         MsgKey = "delete_mode_select"
	MsgDeleteModeSelected       MsgKey = "delete_mode_selected"
	MsgDeleteModeSelectedCount  MsgKey = "delete_mode_selected_count"
	MsgDeleteModeDeleteSelected MsgKey = "delete_mode_delete_selected"
	MsgDeleteModeCancel         MsgKey = "delete_mode_cancel"
	MsgDeleteModeConfirmTitle   MsgKey = "delete_mode_confirm_title"
	MsgDeleteModeConfirmButton  MsgKey = "delete_mode_confirm_button"
	MsgDeleteModeBackButton     MsgKey = "delete_mode_back_button"
	MsgDeleteModeEmptySelection MsgKey = "delete_mode_empty_selection"
	MsgDeleteModeResultTitle    MsgKey = "delete_mode_result_title"
	MsgDeleteModeDeletingTitle  MsgKey = "delete_mode_deleting_title"
	MsgDeleteModeDeletingBody   MsgKey = "delete_mode_deleting_body"
	MsgDeleteModeMissingSession MsgKey = "delete_mode_missing_session"

	MsgSwitchSuccess   MsgKey = "switch_success"
	MsgSwitchNoMatch   MsgKey = "switch_no_match"
	MsgSwitchNoSession MsgKey = "switch_no_session"

	MsgCommandTimeout MsgKey = "command_timeout"

	MsgBannedWordBlocked MsgKey = "banned_word_blocked"
	MsgCommandDisabled   MsgKey = "command_disabled"
	MsgAdminRequired     MsgKey = "admin_required"
	MsgRateLimited       MsgKey = "rate_limited"
	MsgPsSent            MsgKey = "ps_sent"
	MsgPsSendFailed      MsgKey = "ps_send_failed"
	MsgPsEmpty           MsgKey = "ps_empty"
	MsgPsNoSession       MsgKey = "ps_no_session"

	MsgWhoamiTitle     MsgKey = "whoami_title"
	MsgWhoamiCardTitle MsgKey = "whoami_card_title"
	MsgWhoamiName      MsgKey = "whoami_name"
	MsgWhoamiPlatform  MsgKey = "whoami_platform"
	MsgWhoamiUsage     MsgKey = "whoami_usage"

	MsgRelayNoBinding     MsgKey = "relay_no_binding"
	MsgRelayBound         MsgKey = "relay_bound"
	MsgRelayBindRemoved   MsgKey = "relay_bind_removed"
	MsgRelayBindNotFound  MsgKey = "relay_bind_not_found"
	MsgRelayBindSuccess   MsgKey = "relay_bind_success"
	MsgRelayUsage         MsgKey = "relay_usage"
	MsgRelayNotAvailable  MsgKey = "relay_not_available"
	MsgRelayUnbound       MsgKey = "relay_unbound"
	MsgRelayBindSelf      MsgKey = "relay_bind_self"
	MsgRelayNotFound      MsgKey = "relay_not_found"
	MsgRelayNoTarget      MsgKey = "relay_no_target"
	MsgRelaySetupHint     MsgKey = "relay_setup_hint"
	MsgRelaySetupOK       MsgKey = "relay_setup_ok"
	MsgRelaySetupExists   MsgKey = "relay_setup_exists"
	MsgRelaySetupNoMemory MsgKey = "relay_setup_no_memory"
	MsgSetupNative        MsgKey = "setup_native"
	MsgCronSetupOK        MsgKey = "cron_setup_ok"

	MsgSearchUsage    MsgKey = "search_usage"
	MsgSearchError    MsgKey = "search_error"
	MsgSearchNoResult MsgKey = "search_no_result"
	MsgSearchResult   MsgKey = "search_result"
	MsgSearchHint     MsgKey = "search_hint"

	MsgBuiltinCmdNew       MsgKey = "new"
	MsgBuiltinCmdList      MsgKey = "list"
	MsgBuiltinCmdSearch    MsgKey = "search"
	MsgBuiltinCmdSwitch    MsgKey = "switch"
	MsgBuiltinCmdDelete    MsgKey = "delete"
	MsgBuiltinCmdName      MsgKey = "name"
	MsgBuiltinCmdCurrent   MsgKey = "current"
	MsgBuiltinCmdHistory   MsgKey = "history"
	MsgBuiltinCmdProvider  MsgKey = "provider"
	MsgBuiltinCmdMemory    MsgKey = "memory"
	MsgBuiltinCmdAllow     MsgKey = "allow"
	MsgBuiltinCmdModel     MsgKey = "model"
	MsgBuiltinCmdReasoning MsgKey = "reasoning"
	MsgBuiltinCmdMode      MsgKey = "mode"
	MsgBuiltinCmdLang      MsgKey = "lang"
	MsgBuiltinCmdQuiet     MsgKey = "quiet"
	MsgBuiltinCmdCompress  MsgKey = "compress"
	MsgBuiltinCmdStop      MsgKey = "stop"
	MsgBuiltinCmdCron      MsgKey = "cron"
	MsgBuiltinCmdCommands  MsgKey = "commands"
	MsgBuiltinCmdAlias     MsgKey = "alias"
	MsgBuiltinCmdSkills    MsgKey = "skills"
	MsgBuiltinCmdConfig    MsgKey = "config"
	MsgBuiltinCmdDoctor    MsgKey = "doctor"
	MsgBuiltinCmdUpgrade   MsgKey = "upgrade"
	MsgBuiltinCmdRestart   MsgKey = "restart"
	MsgBuiltinCmdStatus    MsgKey = "status"
	MsgBuiltinCmdUsage     MsgKey = "usage"
	MsgBuiltinCmdVersion   MsgKey = "version"
	MsgBuiltinCmdHelp      MsgKey = "help"
	MsgBuiltinCmdBind      MsgKey = "bind"
	MsgBuiltinCmdShell     MsgKey = "shell"
	MsgBuiltinCmdDir       MsgKey = "dir"
	MsgBuiltinCmdDiff      MsgKey = "diff"
	MsgBuiltinCmdPs        MsgKey = "ps"

	MsgDiffEmpty       MsgKey = "diff_empty"
	MsgDiffNoDiff2HTML MsgKey = "diff_no_diff2html"

	MsgDirChanged          MsgKey = "dir_changed"
	MsgDirCurrent          MsgKey = "dir_current"
	MsgDirReset            MsgKey = "dir_reset"
	MsgDirUsage            MsgKey = "dir_usage"
	MsgDirNotSupported     MsgKey = "dir_not_supported"
	MsgDirInvalidPath      MsgKey = "dir_invalid_path"
	MsgDirHistoryTitle     MsgKey = "dir_history_title"
	MsgDirHistoryHint      MsgKey = "dir_history_hint"
	MsgDirInvalidIndex     MsgKey = "dir_invalid_index"
	MsgDirNoHistory        MsgKey = "dir_no_history"
	MsgDirNoPrevious       MsgKey = "dir_no_previous"
	MsgDirCardTitle        MsgKey = "dir_card_title"
	MsgDirCardPageHint     MsgKey = "dir_card_page_hint"
	MsgDirCardEmptyHistory MsgKey = "dir_card_empty_history"
	MsgDirCardReset        MsgKey = "dir_card_reset"
	MsgDirCardPrev         MsgKey = "dir_card_prev"
	MsgShow                MsgKey = "show"
	MsgShowUsage           MsgKey = "show_usage"
	MsgShowParseError      MsgKey = "show_parse_error"
	MsgShowNotFound        MsgKey = "show_not_found"
	MsgShowDirWithLocation MsgKey = "show_dir_with_location"
	MsgShowReadFailed      MsgKey = "show_read_failed"

	// Multi-workspace messages
	MsgBuiltinCmdWorkspace       MsgKey = "workspace"
	MsgWsPickerDescription       MsgKey = "ws_picker_description"
	MsgWsPickerTitle             MsgKey = "ws_picker_title"
	MsgWsPickerRoot              MsgKey = "ws_picker_root"
	MsgWsPickerCurrent           MsgKey = "ws_picker_current"
	MsgWsPickerEmpty             MsgKey = "ws_picker_empty"
	MsgWsPickerSelect            MsgKey = "ws_picker_select"
	MsgWsPickerSelected          MsgKey = "ws_picker_selected"
	MsgWsPickerHint              MsgKey = "ws_picker_hint"
	MsgWsPickerStale             MsgKey = "ws_picker_stale"
	MsgWsNotEnabled              MsgKey = "ws_not_enabled"
	MsgWsNoBinding               MsgKey = "ws_no_binding"
	MsgWsInfo                    MsgKey = "ws_info"
	MsgWsInfoShared              MsgKey = "ws_info_shared"
	MsgWsUsage                   MsgKey = "ws_usage"
	MsgWsInitUsage               MsgKey = "ws_init_usage"
	MsgWsBindUsage               MsgKey = "ws_bind_usage"
	MsgWsBindSuccess             MsgKey = "ws_bind_success"
	MsgWsBindNotFound            MsgKey = "ws_bind_not_found"
	MsgWsRouteUsage              MsgKey = "ws_route_usage"
	MsgWsRouteSuccess            MsgKey = "ws_route_success"
	MsgWsRouteAbsoluteRequired   MsgKey = "ws_route_absolute_required"
	MsgWsRouteNotFound           MsgKey = "ws_route_not_found"
	MsgWsRouteNotDirectory       MsgKey = "ws_route_not_directory"
	MsgWsUnbindSuccess           MsgKey = "ws_unbind_success"
	MsgWsListEmpty               MsgKey = "ws_list_empty"
	MsgWsListTitle               MsgKey = "ws_list_title"
	MsgWsSharedNoBinding         MsgKey = "ws_shared_no_binding"
	MsgWsSharedUsage             MsgKey = "ws_shared_usage"
	MsgWsSharedBindSuccess       MsgKey = "ws_shared_bind_success"
	MsgWsSharedRouteSuccess      MsgKey = "ws_shared_route_success"
	MsgWsSharedUnbindSuccess     MsgKey = "ws_shared_unbind_success"
	MsgWsSharedListEmpty         MsgKey = "ws_shared_list_empty"
	MsgWsSharedListTitle         MsgKey = "ws_shared_list_title"
	MsgWsSharedOnlyHint          MsgKey = "ws_shared_only_hint"
	MsgWsNotFoundHint            MsgKey = "ws_not_found_hint"
	MsgWsNotFoundHintGitOnly     MsgKey = "ws_not_found_hint_git_only"
	MsgWsResolutionError         MsgKey = "ws_resolution_error"
	MsgWsCloneProgress           MsgKey = "ws_clone_progress"
	MsgWsCloneSuccess            MsgKey = "ws_clone_success"
	MsgWsCloneFailed             MsgKey = "ws_clone_failed"
	MsgWsInitDirNotFound         MsgKey = "ws_init_dir_not_found"
	MsgWsInitInvalidTarget       MsgKey = "ws_init_invalid_target"
	MsgWsInitLocalPathsDisabled  MsgKey = "ws_init_local_paths_disabled"
	MsgWsWorktreeUsage           MsgKey = "ws_worktree_usage"
	MsgWsWorktreeNotRepo         MsgKey = "ws_worktree_not_repo"
	MsgWsWorktreeListTitle       MsgKey = "ws_worktree_list_title"
	MsgWsWorktreeMainLabel       MsgKey = "ws_worktree_main_label"
	MsgWsWorktreeInvalidName     MsgKey = "ws_worktree_invalid_name"
	MsgWsWorktreeSwitched        MsgKey = "ws_worktree_switched"
	MsgWsWorktreePathTaken       MsgKey = "ws_worktree_path_taken"
	MsgWsWorktreeCreated         MsgKey = "ws_worktree_created"
	MsgWsWorktreeCreatedExisting MsgKey = "ws_worktree_created_existing"
	MsgWsWorktreeFailed          MsgKey = "ws_worktree_failed"
	MsgWsWorktreeNotFound        MsgKey = "ws_worktree_not_found"
	MsgWsWorktreeBusy            MsgKey = "ws_worktree_busy"
	MsgWsWorktreeDirty           MsgKey = "ws_worktree_dirty"
	MsgWsWorktreeRemoved         MsgKey = "ws_worktree_removed"

	// Agent system-prompt tool sections (Issue cc-connect#1655). These are appended
	// to the agent's own system prompt by core/interfaces.go AgentSystemPromptForLang
	// so that operators running lark-agent-bot with language="zh" see the
	// send / cron / timer / relay tool documentation in their native
	// language. Translation coverage is en + zh for this PR; additional
	// languages fall back to en automatically.
	MsgAgentSendToolPrompt  MsgKey = "agent_send_tool_prompt"
	MsgAgentCronToolPrompt  MsgKey = "agent_cron_tool_prompt"
	MsgAgentTimerToolPrompt MsgKey = "agent_timer_tool_prompt"
	MsgAgentRelayToolPrompt MsgKey = "agent_relay_tool_prompt"
	MsgAgentPeerBotPrompt   MsgKey = "agent_peer_bot_prompt"
	// MsgAgentRestartToolPrompt tells the agent to restart lark-agent-bot with
	// `lark-agent-bot restart`, which waits for its turn to end, instead of
	// stopping the process it runs in.
	MsgAgentRestartToolPrompt MsgKey = "agent_restart_tool_prompt"
)

var messages = map[MsgKey]map[Language]string{
	MsgSetupPrepared: {
		LangEnglish:            "Shared template: %d app scopes, %d user scopes, plus events and card callbacks. Review and confirm on the scan page.",
		LangChinese:            "统一模板：%d 项应用权限、%d 项用户权限，以及事件和卡片回调；扫码后统一确认。",
		LangTraditionalChinese: "統一範本：%d 項應用權限、%d 項使用者權限，以及事件和卡片回呼；掃碼後統一確認。",
		LangJapanese:           "共通テンプレート：アプリ権限 %d 件、ユーザー権限 %d 件、イベントとカードコールバック。スキャン画面で確認してください。",
		LangSpanish:            "Plantilla común: %d permisos de aplicación, %d de usuario, eventos y callbacks de tarjetas. Confirma al escanear.",
	},
	MsgSetupIncomplete: {
		LangEnglish:            "Credentials saved; setup verification incomplete: %v. Resolve the listed items and run feishu check; do not create another app.",
		LangChinese:            "凭证已保存，但配置核验未完成：%v。处理缺项后运行 feishu check，无需重复创建。",
		LangTraditionalChinese: "憑證已儲存，但設定核驗未完成：%v。處理缺項後執行 feishu check，無需重複建立。",
		LangJapanese:           "認証情報は保存済みですが設定の検証は未完了です：%v。修正後 feishu check を実行してください。再作成は不要です。",
		LangSpanish:            "Credenciales guardadas; verificación incompleta: %v. Corrige y ejecuta feishu check; no crees otra aplicación.",
	},
	MsgSetupMissing: {
		LangEnglish:            "Missing or ungranted configuration: %s (add these permissions, events and callbacks in the developer console, then publish a version)",
		LangChinese:            "缺失或未获授权的配置：%s（在开发者后台开通这些权限、订阅这些事件与回调，然后发布新版本）",
		LangTraditionalChinese: "缺少或未獲授權的設定：%s（在開發者後台開通這些權限、訂閱這些事件與回呼，然後發布新版本）",
		LangJapanese:           "不足または未承認の設定：%s（開発者コンソールでこれらの権限・イベント・コールバックを追加し、バージョンを公開してください）",
		LangSpanish:            "Configuración ausente o sin autorizar: %s (añade estos permisos, eventos y callbacks en la consola de desarrolladores y publica una versión)",
	},
	MsgSetupChecked: {
		LangEnglish:            "Bot capability and template permissions verified.",
		LangChinese:            "机器人能力及模板权限核验通过。",
		LangTraditionalChinese: "機器人能力及範本權限核驗通過。",
		LangJapanese:           "Bot 機能とテンプレート権限を検証しました。",
		LangSpanish:            "Capacidad del bot y permisos verificados.",
	},
	MsgSetupSubscriptionsUnknown: {
		LangEnglish:            "The API omitted subscription details; events/callbacks could not be verified. Test a message and a /help card button after startup.",
		LangChinese:            "接口未返回订阅详情，无法核验事件和回调；启动后需用一条消息和 /help 卡片按钮验证。",
		LangTraditionalChinese: "介面未回傳訂閱詳情，無法核驗事件和回呼；啟動後請用一則訊息和 /help 卡片按鈕驗證。",
		LangJapanese:           "API が購読詳細を返さないためイベントとコールバックは未検証です。起動後メッセージと /help カードボタンを確認してください。",
		LangSpanish:            "La API omitió las suscripciones; eventos y callbacks sin verificar. Prueba un mensaje y un botón de /help tras iniciar.",
	},
	MsgSetupOwnerUnknown: {
		LangEnglish:            "Could not verify the app owner's open_id; admin_from was not initialized automatically. Use /whoami to finish owner configuration.",
		LangChinese:            "无法核实应用所有者 open_id，未自动初始化 admin_from；请用 /whoami 完成管理员配置。",
		LangTraditionalChinese: "無法核實應用擁有者 open_id，未自動初始化 admin_from；請用 /whoami 完成管理員設定。",
		LangJapanese:           "所有者の open_id を確認できないため admin_from は未設定です。/whoami で管理者を設定してください。",
		LangSpanish:            "No se verificó el open_id del propietario; admin_from no se inicializó. Usa /whoami para configurar al administrador.",
	},
	MsgSetupTargetOccupied: {
		LangEnglish:            "Project %q already has an app. Choose a new project for creation; use feishu check to inspect the existing app.",
		LangChinese:            "项目 %q 已绑定应用。新建请使用新项目名；检查已有应用请用 feishu check。",
		LangTraditionalChinese: "專案 %q 已綁定應用。建立請使用新專案名；檢查已有應用請用 feishu check。",
		LangJapanese:           "プロジェクト %q はアプリに紐付いています。新規名を使うか feishu check で既存アプリを確認してください。",
		LangSpanish:            "El proyecto %q ya tiene aplicación. Usa otro nombre para crear o feishu check para verificar.",
	},
	MsgSetupMenuNotice: {
		LangEnglish:            "Menu contents are not included in registration. Publication and visibility remain subject to platform/tenant policy.",
		LangChinese:            "注册模板不包含菜单内容；发布与可用范围仍以平台及企业策略为准。",
		LangTraditionalChinese: "註冊範本不包含選單內容；發布與可用範圍仍以平台及企業策略為準。",
		LangJapanese:           "登録テンプレートにメニュー内容は含まれません。公開と利用範囲にはプラットフォームと組織の方針が適用されます。",
		LangSpanish:            "La plantilla no incluye el contenido del menú; publicación y visibilidad dependen de la plataforma y la organización.",
	},
	MsgSetupProjectCreated: {
		LangEnglish:            "Created project %q with agent %s (use --agent to choose another).",
		LangChinese:            "已自动创建项目 %q，agent 为 %s（可用 --agent 指定其他 agent）。",
		LangTraditionalChinese: "已自動建立專案 %q，agent 為 %s（可用 --agent 指定其他 agent）。",
		LangJapanese:           "プロジェクト %q を作成しました。agent は %s です（--agent で変更できます）。",
		LangSpanish:            "Proyecto %q creado con el agente %s (usa --agent para elegir otro).",
	},
	MsgSetupStarterFilled: {
		LangEnglish:            "Filled in the starter project %q from the default config; agent: %s.",
		LangChinese:            "已填入默认配置中的初始项目 %q，agent 为 %s。",
		LangTraditionalChinese: "已填入預設設定中的初始專案 %q，agent 為 %s。",
		LangJapanese:           "既定の設定にある初期プロジェクト %q を設定しました。agent は %s です。",
		LangSpanish:            "Se completó el proyecto inicial %q de la configuración por defecto; agente: %s.",
	},
	MsgSetupPlatformAdded: {
		LangEnglish:            "Project %q had no Feishu/Lark platform, added one automatically.",
		LangChinese:            "项目 %q 没有飞书/Lark 平台，已自动添加。",
		LangTraditionalChinese: "專案 %q 沒有飛書/Lark 平台，已自動新增。",
		LangJapanese:           "プロジェクト %q に Feishu/Lark プラットフォームがなかったため、自動で追加しました。",
		LangSpanish:            "El proyecto %q no tenía plataforma Feishu/Lark; se añadió una automáticamente.",
	},
	MsgSetupWorkDirFilled: {
		LangEnglish:            "Set work_dir of project %q to %s.",
		LangChinese:            "已将项目 %q 的 work_dir 设为 %s。",
		LangTraditionalChinese: "已將專案 %q 的 work_dir 設為 %s。",
		LangJapanese:           "プロジェクト %q の work_dir を %s に設定しました。",
		LangSpanish:            "work_dir del proyecto %q establecido en %s.",
	},
	MsgSetupConfigCreated: {
		LangEnglish:            "Created default config at %s\nEdit it to add your agent and platform credentials, then run lark-agent-bot again.\nOr create a Feishu/Lark bot by scanning a QR code, from the folder the agent should work in:\n  lark-agent-bot feishu setup --project my-project",
		LangChinese:            "已在 %s 创建默认配置。\n编辑该文件填入 agent 和平台凭证后，再次运行 lark-agent-bot。\n也可以在 agent 要工作的目录下扫码创建飞书/Lark 机器人：\n  lark-agent-bot feishu setup --project my-project",
		LangTraditionalChinese: "已在 %s 建立預設設定。\n編輯該檔案填入 agent 和平台憑證後，再次執行 lark-agent-bot。\n也可以在 agent 要工作的目錄下掃碼建立飛書/Lark 機器人：\n  lark-agent-bot feishu setup --project my-project",
		LangJapanese:           "既定の設定を %s に作成しました。\nagent とプラットフォームの認証情報を記入してから、lark-agent-bot を再度実行してください。\nまたは agent が作業するフォルダで、QR コードをスキャンして Feishu/Lark ボットを作成できます：\n  lark-agent-bot feishu setup --project my-project",
		LangSpanish:            "Configuración por defecto creada en %s\nEdítala con tu agente y las credenciales de la plataforma y vuelve a ejecutar lark-agent-bot.\nO crea un bot de Feishu/Lark escaneando un código QR, desde la carpeta donde trabajará el agente:\n  lark-agent-bot feishu setup --project my-project",
	},
	MsgSetupNoProjects: {
		LangEnglish:            "Error: no projects configured in %s\nAdd at least one [[projects]] section to it, or create a Feishu/Lark bot by scanning a QR code, from the folder the agent should work in:\n  lark-agent-bot feishu setup --project my-project",
		LangChinese:            "错误：%s 中没有配置任何项目。\n请在其中添加至少一个 [[projects]]，或在 agent 要工作的目录下扫码创建飞书/Lark 机器人：\n  lark-agent-bot feishu setup --project my-project",
		LangTraditionalChinese: "錯誤：%s 中沒有設定任何專案。\n請在其中新增至少一個 [[projects]]，或在 agent 要工作的目錄下掃碼建立飛書/Lark 機器人：\n  lark-agent-bot feishu setup --project my-project",
		LangJapanese:           "エラー：%s にプロジェクトが設定されていません。\n[[projects]] を1つ以上追加するか、agent が作業するフォルダで QR コードをスキャンして Feishu/Lark ボットを作成してください：\n  lark-agent-bot feishu setup --project my-project",
		LangSpanish:            "Error: no hay proyectos configurados en %s\nAñade al menos una sección [[projects]] o crea un bot de Feishu/Lark escaneando un código QR, desde la carpeta donde trabajará el agente:\n  lark-agent-bot feishu setup --project my-project",
	},
	MsgSetupMenuGuidance: {
		LangEnglish:            "Menu setup remains to be completed in the developer console (registration does not create menu items):\n1. Open %s and select your app → Bot → Custom menu.\n2. Enable the floating menu and add these three top-level items, all using Push event:\n   View help → event_key: help\n   Current status → event_key: status\n   Upgrade service → event_key: upgrade\n3. Confirm application.bot.menu_v6, im.message.recalled_v1 and im.chat.access_event.bot_p2p_chat_entered_v1 are subscribed under Events & callbacks.\n4. Create and publish a version. Menu changes may take about 5 minutes to appear.\n",
		LangChinese:            "菜单待完成：注册不会创建菜单项，请在开发者后台完成以下步骤：\n1. 打开 %s，选择应用 → 机器人 → 机器人自定义菜单。\n2. 开启悬浮菜单，添加三个主菜单，响应动作均选择「推送事件」：\n   查看帮助 → event_key: help\n   当前状态 → event_key: status\n   升级服务 → event_key: upgrade\n3. 在事件与回调中确认已订阅 application.bot.menu_v6、im.message.recalled_v1 和 im.chat.access_event.bot_p2p_chat_entered_v1。\n4. 创建版本并发布，菜单显示可能需要约 5 分钟。\n",
		LangTraditionalChinese: "選單待完成：註冊不會建立選單項目，請在開發者後台完成以下步驟：\n1. 開啟 %s，選擇應用 → 機器人 → 機器人自訂選單。\n2. 啟用懸浮選單，新增三個主選單，回應動作均選擇「推送事件」：\n   查看說明 → event_key: help\n   目前狀態 → event_key: status\n   升級服務 → event_key: upgrade\n3. 在事件與回呼中確認已訂閱 application.bot.menu_v6、im.message.recalled_v1 和 im.chat.access_event.bot_p2p_chat_entered_v1。\n4. 建立版本並發布，選單顯示可能需要約 5 分鐘。\n",
		LangJapanese:           "メニュー設定は開発者コンソールで完了してください（登録ではメニュー項目は作成されません）：\n1. %s でアプリ → ボット → カスタムメニューを開きます。\n2. フローティングメニューを有効にし、次の3項目を追加します。すべて「イベントを送信」を選択してください：\n   ヘルプ → event_key: help\n   現在の状態 → event_key: status\n   サービスを更新 → event_key: upgrade\n3. イベントとコールバックで application.bot.menu_v6、im.message.recalled_v1、im.chat.access_event.bot_p2p_chat_entered_v1 の購読を確認します。\n4. バージョンを作成して公開します。表示には約5分かかる場合があります。\n",
		LangSpanish:            "Falta configurar el menú en la consola de desarrolladores (el registro no crea sus elementos):\n1. Abre %s y selecciona tu aplicación → Bot → Menú personalizado.\n2. Activa el menú flotante y añade estos tres elementos principales, todos con la acción Enviar evento:\n   Ver ayuda → event_key: help\n   Estado actual → event_key: status\n   Actualizar servicio → event_key: upgrade\n3. Confirma las suscripciones a application.bot.menu_v6, im.message.recalled_v1 e im.chat.access_event.bot_p2p_chat_entered_v1 en Eventos y callbacks.\n4. Crea y publica una versión. El menú puede tardar unos 5 minutos en aparecer.\n",
	},
	MsgSetupScanQR: {
		LangEnglish:            "Scan this QR code with the Feishu/Lark mobile app to create and authorize the bot:",
		LangChinese:            "请使用飞书/Lark 手机 App 扫码完成机器人创建与授权：",
		LangTraditionalChinese: "請使用飛書/Lark 手機 App 掃碼完成機器人建立與授權：",
		LangJapanese:           "Feishu/Lark のモバイルアプリでこの QR コードをスキャンし、ボットの作成と承認を完了してください：",
		LangSpanish:            "Escanea este código QR con la aplicación móvil de Feishu/Lark para crear y autorizar el bot:",
	},
	MsgCLIWebStarting: {
		LangEnglish:            "Starting lark-agent-bot with %s. The browser will open when ready. Keep this terminal open; Ctrl+C stops the bot.",
		LangChinese:            "正在使用 %s 启动 lark-agent-bot，就绪后会打开浏览器。请保持此终端开启，按 Ctrl+C 停止服务。",
		LangTraditionalChinese: "正在使用 %s 啟動 lark-agent-bot，就緒後會開啟瀏覽器。請保持此終端開啟，按 Ctrl+C 停止服務。",
		LangJapanese:           "%s で lark-agent-bot を起動しています。準備ができたらブラウザを開きます。この端末を開いたままにしてください。Ctrl+C で停止します。",
		LangSpanish:            "Iniciando lark-agent-bot con %s. El navegador se abrirá cuando esté listo. Mantén esta terminal abierta; Ctrl+C detiene el bot.",
	},
	MsgCLIWebManualStart: {
		LangEnglish:            "Configuration only; no server was started. Start it with: lark-agent-bot --config %s (restart the bot if already running).",
		LangChinese:            "这里只完成配置，尚未启动服务。启动命令：lark-agent-bot --config %s（如机器人已运行，请重启该机器人）。",
		LangTraditionalChinese: "這裡只完成設定，尚未啟動服務。啟動指令：lark-agent-bot --config %s（如機器人已執行，請重新啟動該機器人）。",
		LangJapanese:           "設定のみ完了しました。サーバーは起動していません。起動コマンド: lark-agent-bot --config %s（すでに動作中なら再起動してください）。",
		LangSpanish:            "Solo se configuró; no se inició el servidor. Inícialo con: lark-agent-bot --config %s (reinicia el bot si ya está en ejecución).",
	},
	MsgCLIWebRunningNeedsRestart: {
		LangEnglish:            "A bot using %s is already running without a ready web server. Restart that bot to apply the web settings, then run web again.",
		LangChinese:            "使用 %s 的机器人已在运行，但 Web 服务尚未就绪。请重启该机器人使 Web 配置生效，再运行 web。",
		LangTraditionalChinese: "使用 %s 的機器人已在執行，但 Web 服務尚未就緒。請重新啟動該機器人使 Web 設定生效，再執行 web。",
		LangJapanese:           "%s を使うボットは動作中ですが、Web サーバーは準備できていません。ボットを再起動して Web 設定を反映し、web を再実行してください。",
		LangSpanish:            "Ya hay un bot usando %s, pero el servidor web no está listo. Reinicia ese bot para aplicar la configuración web y ejecuta web de nuevo.",
	},
	MsgCLIWebUnavailable: {
		LangEnglish:            "Web admin at %s is not ready: %v. Check the port and token in the selected config; restart its bot if the web settings changed.",
		LangChinese:            "%s 的 Web 管理后台尚不可用：%v。请检查所选配置的端口和令牌；如修改了 Web 配置，请重启对应机器人。",
		LangTraditionalChinese: "%s 的 Web 管理後台尚無法使用：%v。請檢查所選設定的連接埠和權杖；如修改了 Web 設定，請重新啟動對應機器人。",
		LangJapanese:           "%s の Web 管理画面は準備できていません: %v。選択した設定のポートとトークンを確認し、Web 設定を変更した場合はボットを再起動してください。",
		LangSpanish:            "La administración web en %s no está lista: %v. Revisa el puerto y el token de la configuración seleccionada; reinicia su bot si cambiaste la configuración web.",
	},
	MsgCLIWebNotBuilt: {
		LangEnglish:            "The web admin is not in this build of lark-agent-bot. Use a release binary, or build with `make build`, which builds the web admin first.",
		LangChinese:            "当前 lark-agent-bot 构建不包含 Web 管理后台。请使用发布版二进制，或用 `make build` 构建（会先构建 Web 管理后台）。",
		LangTraditionalChinese: "目前的 lark-agent-bot 建置不包含 Web 管理後台。請使用正式發佈的執行檔，或用 `make build` 建置（會先建置 Web 管理後台）。",
		LangJapanese:           "この lark-agent-bot のビルドには Web 管理画面が含まれていません。リリース版のバイナリを使うか、`make build` でビルドしてください（先に Web 管理画面をビルドします）。",
		LangSpanish:            "Esta compilación de lark-agent-bot no incluye la administración web. Usa un binario publicado o compila con `make build`, que compila antes la administración web.",
	},
	MsgCLIWebConfigCreated: {
		LangEnglish:            "Created a default config at %s.",
		LangChinese:            "已在 %s 创建默认配置。",
		LangTraditionalChinese: "已在 %s 建立預設設定。",
		LangJapanese:           "既定の設定を %s に作成しました。",
		LangSpanish:            "Configuración por defecto creada en %s.",
	},
	MsgCLIWebEnabling: {
		LangEnglish:            "The web admin is off. Turning it on...",
		LangChinese:            "Web 管理后台未开启，正在开启……",
		LangTraditionalChinese: "Web 管理後台未開啟，正在開啟……",
		LangJapanese:           "Web 管理画面がオフです。オンにしています……",
		LangSpanish:            "La administración web está desactivada. Activándola...",
	},
	MsgCLIWebEnabled: {
		LangEnglish:            "Web admin turned on at port %d in %s.",
		LangChinese:            "已在 %[2]s 中开启 Web 管理后台，端口 %[1]d。",
		LangTraditionalChinese: "已在 %[2]s 中開啟 Web 管理後台，連接埠 %[1]d。",
		LangJapanese:           "%[2]s で Web 管理画面をオンにしました（ポート %[1]d）。",
		LangSpanish:            "Administración web activada en el puerto %d, en %s.",
	},
	MsgCLIWebOpening: {
		LangEnglish:            "Opening %s",
		LangChinese:            "正在打开 %s",
		LangTraditionalChinese: "正在開啟 %s",
		LangJapanese:           "%s を開いています",
		LangSpanish:            "Abriendo %s",
	},
	MsgCLIWebOpenFailed: {
		LangEnglish:            "\nCould not open a browser. Open this URL in your browser:\n  %s\n\nlark-agent-bot must be running: it serves the web admin on port %d.",
		LangChinese:            "\n无法自动打开浏览器，请在浏览器中打开：\n  %s\n\nlark-agent-bot 需要在运行：Web 管理后台由它在端口 %d 上提供。",
		LangTraditionalChinese: "\n無法自動開啟瀏覽器，請在瀏覽器中開啟：\n  %s\n\nlark-agent-bot 需要在執行：Web 管理後台由它在連接埠 %d 上提供。",
		LangJapanese:           "\nブラウザを開けませんでした。次の URL をブラウザで開いてください：\n  %s\n\nlark-agent-bot が起動している必要があります（ポート %d で Web 管理画面を提供します）。",
		LangSpanish:            "\nNo se pudo abrir un navegador. Abre esta URL en tu navegador:\n  %s\n\nlark-agent-bot debe estar en ejecución: sirve la administración web en el puerto %d.",
	},
	MsgCLIDoctorConfigMissing: {
		LangEnglish:            "Config file %s not found. Run lark-agent-bot once to create a starter config, or create a bot with: %s",
		LangChinese:            "未找到配置文件 %s。运行一次 lark-agent-bot 生成初始配置，或用以下命令创建机器人：%s",
		LangTraditionalChinese: "找不到設定檔 %s。執行一次 lark-agent-bot 產生初始設定，或用以下指令建立機器人：%s",
		LangJapanese:           "設定ファイル %s が見つかりません。lark-agent-bot を一度実行して初期設定を作成するか、次のコマンドでボットを作成してください：%s",
		LangSpanish:            "No se encontró el archivo de configuración %s. Ejecuta lark-agent-bot una vez para crear una configuración inicial, o crea un bot con: %s",
	},
	MsgCLIDoctorConfigInvalid: {
		LangEnglish:            "Config file %s cannot be loaded: %v",
		LangChinese:            "配置文件 %s 无法加载：%v",
		LangTraditionalChinese: "設定檔 %s 無法載入：%v",
		LangJapanese:           "設定ファイル %s を読み込めません：%v",
		LangSpanish:            "No se puede cargar el archivo de configuración %s: %v",
	},
	MsgCLIDoctorConfigOK: {
		LangEnglish:            "Config file: %s",
		LangChinese:            "配置文件：%s",
		LangTraditionalChinese: "設定檔：%s",
		LangJapanese:           "設定ファイル：%s",
		LangSpanish:            "Archivo de configuración: %s",
	},
	MsgCLIDoctorNoProjects: {
		LangEnglish:            "The config has no [[projects]]. Add one, or create a bot with: %s",
		LangChinese:            "配置中没有任何 [[projects]]。请添加一个，或用以下命令创建机器人：%s",
		LangTraditionalChinese: "設定中沒有任何 [[projects]]。請新增一個，或用以下指令建立機器人：%s",
		LangJapanese:           "設定に [[projects]] がありません。追加するか、次のコマンドでボットを作成してください：%s",
		LangSpanish:            "La configuración no tiene [[projects]]. Añade uno o crea un bot con: %s",
	},
	MsgCLIDoctorProjects: {
		LangEnglish:            "Projects (%d): %s",
		LangChinese:            "项目（%d 个）：%s",
		LangTraditionalChinese: "專案（%d 個）：%s",
		LangJapanese:           "プロジェクト（%d 件）：%s",
		LangSpanish:            "Proyectos (%d): %s",
	},
	MsgCLIDoctorWorkDirOK: {
		LangEnglish:            "work_dir: %s",
		LangChinese:            "work_dir：%s",
		LangTraditionalChinese: "work_dir：%s",
		LangJapanese:           "work_dir：%s",
		LangSpanish:            "work_dir: %s",
	},
	MsgCLIDoctorWorkDirUnset: {
		LangEnglish:            "work_dir is not set: the agent works in the folder lark-agent-bot starts in.",
		LangChinese:            "未设置 work_dir：agent 将在 lark-agent-bot 启动时所在的目录中工作。",
		LangTraditionalChinese: "未設定 work_dir：agent 會在 lark-agent-bot 啟動時所在的目錄中工作。",
		LangJapanese:           "work_dir が未設定です：agent は lark-agent-bot を起動したフォルダで作業します。",
		LangSpanish:            "work_dir no está definido: el agente trabaja en la carpeta donde se inicia lark-agent-bot.",
	},
	MsgCLIDoctorWorkDirPlaceholder: {
		LangEnglish:            "work_dir is still the placeholder %s. Set it under [projects.agent.options] to the folder the agent should work in.",
		LangChinese:            "work_dir 仍是占位值 %s。请在 [projects.agent.options] 中把它设为 agent 要工作的目录。",
		LangTraditionalChinese: "work_dir 仍是預留值 %s。請在 [projects.agent.options] 中把它設為 agent 要工作的目錄。",
		LangJapanese:           "work_dir がプレースホルダー %s のままです。[projects.agent.options] で agent が作業するフォルダを指定してください。",
		LangSpanish:            "work_dir sigue siendo el marcador %s. Defínelo en [projects.agent.options] con la carpeta donde debe trabajar el agente.",
	},
	MsgCLIDoctorWorkDirMissing: {
		LangEnglish:            "work_dir %s does not exist or is not a folder. Set it under [projects.agent.options] to the folder the agent should work in.",
		LangChinese:            "work_dir %s 不存在或不是目录。请在 [projects.agent.options] 中把它设为 agent 要工作的目录。",
		LangTraditionalChinese: "work_dir %s 不存在或不是目錄。請在 [projects.agent.options] 中把它設為 agent 要工作的目錄。",
		LangJapanese:           "work_dir %s が存在しないか、フォルダではありません。[projects.agent.options] で agent が作業するフォルダを指定してください。",
		LangSpanish:            "work_dir %s no existe o no es una carpeta. Defínelo en [projects.agent.options] con la carpeta donde debe trabajar el agente.",
	},
	MsgCLIDoctorAgentOK: {
		LangEnglish:            "Agent type: %s",
		LangChinese:            "Agent 类型：%s",
		LangTraditionalChinese: "Agent 類型：%s",
		LangJapanese:           "Agent の種類：%s",
		LangSpanish:            "Tipo de agente: %s",
	},
	MsgCLIDoctorAgentUnknown: {
		LangEnglish:            "Agent type %q is not in this build (available: %s).",
		LangChinese:            "当前构建不包含 agent 类型 %q（可用：%s）。",
		LangTraditionalChinese: "目前建置不包含 agent 類型 %q（可用：%s）。",
		LangJapanese:           "agent の種類 %q はこのビルドに含まれていません（利用可能：%s）。",
		LangSpanish:            "El tipo de agente %q no está en esta compilación (disponibles: %s).",
	},
	MsgCLIDoctorAgentFailed: {
		LangEnglish:            "Agent %s cannot start: %v",
		LangChinese:            "Agent %s 无法启动：%v",
		LangTraditionalChinese: "Agent %s 無法啟動：%v",
		LangJapanese:           "Agent %s を起動できません：%v",
		LangSpanish:            "El agente %s no puede iniciarse: %v",
	},
	MsgCLIDoctorAgentCLIOK: {
		LangEnglish:            "Agent CLI %s: %s",
		LangChinese:            "Agent 命令行 %s：%s",
		LangTraditionalChinese: "Agent 命令列 %s：%s",
		LangJapanese:           "Agent CLI %s：%s",
		LangSpanish:            "CLI del agente %s: %s",
	},
	MsgCLIDoctorAgentCLIMissing: {
		LangEnglish:            "Agent CLI %s not found in PATH. Install it, or set cmd under [projects.agent.options] to its path.",
		LangChinese:            "PATH 中找不到 agent 命令行 %s。请安装它，或在 [projects.agent.options] 中把 cmd 设为它的路径。",
		LangTraditionalChinese: "PATH 中找不到 agent 命令列 %s。請安裝它，或在 [projects.agent.options] 中把 cmd 設為它的路徑。",
		LangJapanese:           "PATH に agent CLI %s が見つかりません。インストールするか、[projects.agent.options] の cmd にそのパスを指定してください。",
		LangSpanish:            "No se encontró la CLI del agente %s en el PATH. Instálala o define cmd en [projects.agent.options] con su ruta.",
	},
	MsgCLIDoctorAgentCLIFound: {
		LangEnglish:            "Agent CLI: found by the %s agent.",
		LangChinese:            "Agent 命令行：%s agent 已找到。",
		LangTraditionalChinese: "Agent 命令列：%s agent 已找到。",
		LangJapanese:           "Agent CLI：%s agent により検出されました。",
		LangSpanish:            "CLI del agente: encontrada por el agente %s.",
	},
	MsgCLIDoctorAgentCLIRunAs: {
		LangEnglish:            "The agent runs as user %s, so its CLI is not looked up here. Check it with: %s",
		LangChinese:            "Agent 以用户 %s 运行，这里不检查它的命令行。请用以下命令检查：%s",
		LangTraditionalChinese: "Agent 以使用者 %s 執行，這裡不檢查它的命令列。請用以下指令檢查：%s",
		LangJapanese:           "agent はユーザー %s として実行されるため、ここでは CLI を確認しません。次のコマンドで確認してください：%s",
		LangSpanish:            "El agente se ejecuta como el usuario %s, así que aquí no se busca su CLI. Compruébala con: %s",
	},
	MsgCLIDoctorPlatformUnknown: {
		LangEnglish:            "Platform type %q is not in this build (available: %s).",
		LangChinese:            "当前构建不包含平台类型 %q（可用：%s）。",
		LangTraditionalChinese: "目前建置不包含平台類型 %q（可用：%s）。",
		LangJapanese:           "プラットフォームの種類 %q はこのビルドに含まれていません（利用可能：%s）。",
		LangSpanish:            "El tipo de plataforma %q no está en esta compilación (disponibles: %s).",
	},
	MsgCLIDoctorCredentialsEmpty: {
		LangEnglish:            "%s app_id or app_secret is empty. Fill them in, or create a bot with: %s",
		LangChinese:            "%s 的 app_id 或 app_secret 为空。请填写，或用以下命令创建机器人：%s",
		LangTraditionalChinese: "%s 的 app_id 或 app_secret 為空。請填寫，或用以下指令建立機器人：%s",
		LangJapanese:           "%s の app_id または app_secret が空です。記入するか、次のコマンドでボットを作成してください：%s",
		LangSpanish:            "app_id o app_secret de %s está vacío. Rellénalos o crea un bot con: %s",
	},
	MsgCLIDoctorCredentialsPlaceholder: {
		LangEnglish:            "%s app_id / app_secret are still the starter placeholders. Fill them in, or create a bot with: %s",
		LangChinese:            "%s 的 app_id / app_secret 仍是初始配置的占位值。请填写，或用以下命令创建机器人：%s",
		LangTraditionalChinese: "%s 的 app_id / app_secret 仍是初始設定的預留值。請填寫，或用以下指令建立機器人：%s",
		LangJapanese:           "%s の app_id / app_secret が初期設定のプレースホルダーのままです。記入するか、次のコマンドでボットを作成してください：%s",
		LangSpanish:            "app_id / app_secret de %s siguen siendo los marcadores de la configuración inicial. Rellénalos o crea un bot con: %s",
	},
	MsgCLIDoctorCredentialsOK: {
		LangEnglish:            "%s app_id %s. Check the app's permissions with: %s",
		LangChinese:            "%s app_id %s。可用以下命令检查应用权限：%s",
		LangTraditionalChinese: "%s app_id %s。可用以下指令檢查應用權限：%s",
		LangJapanese:           "%s app_id %s。次のコマンドでアプリの権限を確認できます：%s",
		LangSpanish:            "%s app_id %s. Comprueba los permisos de la aplicación con: %s",
	},
	MsgStarting: {
		LangEnglish:            "⏳ Processing...",
		LangChinese:            "⏳ 处理中...",
		LangTraditionalChinese: "⏳ 處理中...",
		LangJapanese:           "⏳ 処理中...",
		LangSpanish:            "⏳ Procesando...",
	},
	MsgThinking: {
		LangEnglish: "💭 %s",
		LangChinese: "💭 %s",
	},
	MsgTool: {
		LangEnglish:            "🔧 **Tool #%d: %s**\n---\n%s",
		LangChinese:            "🔧 **工具 #%d: %s**\n---\n%s",
		LangTraditionalChinese: "🔧 **工具 #%d: %s**\n---\n%s",
		LangJapanese:           "🔧 **ツール #%d: %s**\n---\n%s",
		LangSpanish:            "🔧 **Herramienta #%d: %s**\n---\n%s",
	},
	MsgToolResult: {
		LangEnglish:            "📤 **%s**\n---\n%s",
		LangChinese:            "📤 **%s**\n---\n%s",
		LangTraditionalChinese: "📤 **%s**\n---\n%s",
		LangJapanese:           "📤 **%s**\n---\n%s",
		LangSpanish:            "📤 **%s**\n---\n%s",
	},
	MsgToolResultFmtStatus: {
		LangEnglish:            "Status",
		LangChinese:            "状态",
		LangTraditionalChinese: "狀態",
		LangJapanese:           "ステータス",
		LangSpanish:            "Estado",
	},
	MsgToolResultFmtExit: {
		LangEnglish:            "Exit",
		LangChinese:            "退出码",
		LangTraditionalChinese: "結束代碼",
		LangJapanese:           "終了コード",
		LangSpanish:            "Salida",
	},
	MsgToolResultFmtNoOutput: {
		LangEnglish:            "No output",
		LangChinese:            "无输出",
		LangTraditionalChinese: "無輸出",
		LangJapanese:           "出力なし",
		LangSpanish:            "Sin salida",
	},
	MsgToolResultFmtOk: {
		LangEnglish:            "ok",
		LangChinese:            "ok",
		LangTraditionalChinese: "ok",
		LangJapanese:           "ok",
		LangSpanish:            "ok",
	},
	MsgToolResultFmtFailed: {
		LangEnglish:            "failed",
		LangChinese:            "failed",
		LangTraditionalChinese: "failed",
		LangJapanese:           "failed",
		LangSpanish:            "fallido",
	},
	MsgExecutionStopped: {
		LangEnglish:            "⏹ Execution stopped.",
		LangChinese:            "⏹ 执行已停止。",
		LangTraditionalChinese: "⏹ 執行已停止。",
		LangJapanese:           "⏹ 実行を停止しました。",
		LangSpanish:            "⏹ Ejecución detenida.",
	},
	MsgSessionCloseFailed: {
		LangEnglish:            "⚠️ Warning: the stopped session's background process could not be confirmed killed. It may still be running and using its old credentials.",
		LangChinese:            "⚠️ 警告：已停止会话的后台进程未能确认已终止，可能仍在运行并占用原有凭证。",
		LangTraditionalChinese: "⚠️ 警告：已停止會話的後台進程未能確認已終止，可能仍在運行並佔用原有憑證。",
		LangJapanese:           "⚠️ 警告：停止したセッションのバックグラウンドプロセスの終了を確認できませんでした。まだ実行中で、以前の認証情報を使用している可能性があります。",
		LangSpanish:            "⚠️ Advertencia: no se pudo confirmar que el proceso en segundo plano de la sesión detenida haya finalizado. Podría seguir en ejecución usando sus credenciales anteriores.",
	},
	MsgSessionResumeUnsafe: {
		LangEnglish:            "⚠️ The previous process could not be confirmed stopped, so this conversation was NOT resumed — a brand-new session was started instead (earlier context is not carried over). This avoids two agents acting on the same conversation.",
		LangChinese:            "⚠️ 上一个进程未能确认已停止，因此没有续接原会话，已为你开启全新会话（此前上下文未带入）。这是为了避免两个智能体同时操作同一个会话。",
		LangTraditionalChinese: "⚠️ 上一個行程未能確認已停止，因此沒有續接原會話，已為你開啟全新會話（先前上下文未帶入）。這是為了避免兩個智能體同時操作同一個會話。",
		LangJapanese:           "⚠️ 前のプロセスの停止を確認できなかったため、会話を再開せず新しいセッションを開始しました（以前の文脈は引き継がれません）。同じ会話を2つのエージェントが操作するのを防ぐためです。",
		LangSpanish:            "⚠️ No se pudo confirmar que el proceso anterior se detuviera, así que no se reanudó esta conversación: se inició una sesión nueva (sin el contexto anterior). Así se evita que dos agentes actúen sobre la misma conversación.",
	},
	MsgSessionCancelled: {
		LangEnglish:            "Session cancelled. Ready for new instructions.",
		LangChinese:            "会话已取消。可以继续新的对话。",
		LangTraditionalChinese: "會話已取消。可以繼續新的對話。",
		LangJapanese:           "セッションをキャンセルしました。新しい指示を受け付けます。",
		LangSpanish:            "Sesion cancelada. Listo para nuevas instrucciones.",
	},
	MsgNoExecution: {
		LangEnglish:            "No execution in progress.",
		LangChinese:            "没有正在执行的任务。",
		LangTraditionalChinese: "沒有正在執行的任務。",
		LangJapanese:           "実行中のタスクはありません。",
		LangSpanish:            "No hay ejecución en progreso.",
	},
	MsgPreviousProcessing: {
		LangEnglish:            "⏳ Previous request still processing. Use `/ps <message>` to send a P.S. to the running task.",
		LangChinese:            "⏳ 上一个请求仍在处理中。使用 `/ps <消息>` 可向正在执行的任务追加补充信息。",
		LangTraditionalChinese: "⏳ 上一個請求仍在處理中。使用 `/ps <訊息>` 可向正在執行的任務追加補充資訊。",
		LangJapanese:           "⏳ 前のリクエストを処理中です。`/ps <メッセージ>` で実行中のタスクに補足情報を送れます。",
		LangSpanish:            "⏳ La solicitud anterior aún se está procesando. Use `/ps <mensaje>` para enviar un P.S. a la tarea en curso.",
	},
	MsgMessageQueued: {
		LangEnglish:            "📬 Message received — will process after the current task finishes.",
		LangChinese:            "📬 消息已收到，将在当前任务完成后处理。",
		LangTraditionalChinese: "📬 訊息已收到，將在目前任務完成後處理。",
		LangJapanese:           "📬 メッセージを受信しました。現在のタスク完了後に処理します。",
		LangSpanish:            "📬 Mensaje recibido — se procesará después de que termine la tarea actual.",
	},
	MsgRecallQueuedCancelled: {
		LangEnglish:            "✅ Message recalled. The queued task has been cancelled.",
		LangChinese:            "✅ 原消息已撤回，对应的排队任务已取消。",
		LangTraditionalChinese: "✅ 原訊息已撤回，對應的排隊任務已取消。",
		LangJapanese:           "✅ メッセージが取り消され、対応する待機中のタスクをキャンセルしました。",
		LangSpanish:            "✅ Mensaje retirado. Se ha cancelado la tarea correspondiente en cola.",
	},
	MsgRecallActiveStopping: {
		LangEnglish:            "⏹️ Message recalled. Stopping the current task; completed actions will not be undone.",
		LangChinese:            "⏹️ 原消息已撤回，已发起停止当前任务；已执行的操作不会回滚。",
		LangTraditionalChinese: "⏹️ 原訊息已撤回，已發起停止目前任務；已執行的操作不會回復。",
		LangJapanese:           "⏹️ メッセージが取り消されたため、現在のタスクを停止しています。実行済みの操作は元に戻りません。",
		LangSpanish:            "⏹️ Mensaje retirado. Deteniendo la tarea actual; las acciones ya realizadas no se desharán.",
	},
	MsgRecallQueuedDropped: {
		LangEnglish:            "⚠️ The running task was stopped because its message was recalled, so this queued message was not run. Send it again if you still need it.",
		LangChinese:            "⚠️ 正在执行的任务因原消息撤回已停止，这条排队消息未执行，需要的话请重新发送。",
		LangTraditionalChinese: "⚠️ 正在執行的任務因原訊息撤回已停止，這則排隊訊息未執行，需要的話請重新傳送。",
		LangJapanese:           "⚠️ 実行中のタスクは元のメッセージが取り消されたため停止しました。この待機中のメッセージは実行されていません。必要な場合は再送してください。",
		LangSpanish:            "⚠️ La tarea en curso se detuvo porque se retiró su mensaje, así que este mensaje en cola no se ejecutó. Vuelve a enviarlo si aún lo necesitas.",
	},
	MsgQueueFull: {
		LangEnglish:            "📬 Message queue is full (%d pending). Please wait for current tasks to complete.",
		LangChinese:            "📬 消息队列已满（%d 条待处理）。请等待当前任务完成。",
		LangTraditionalChinese: "📬 訊息佇列已滿（%d 則待處理）。請等待目前任務完成。",
		LangJapanese:           "📬 メッセージキューが満杯です（%d 件待ち）。現在のタスク完了をお待ちください。",
		LangSpanish:            "📬 La cola de mensajes está llena (%d pendientes). Espere a que las tareas actuales se completen.",
	},
	MsgNoToolsAllowed: {
		LangEnglish:            "No tools pre-allowed.\nUsage: `/allow <tool_name>`\nExample: `/allow Bash`",
		LangChinese:            "尚未预授权任何工具。\n用法: `/allow <工具名>`\n示例: `/allow Bash`",
		LangTraditionalChinese: "尚未預授權任何工具。\n用法: `/allow <工具名>`\n範例: `/allow Bash`",
		LangJapanese:           "事前許可されたツールはありません。\n使い方: `/allow <ツール名>`\n例: `/allow Bash`",
		LangSpanish:            "No hay herramientas pre-autorizadas.\nUso: `/allow <nombre_herramienta>`\nEjemplo: `/allow Bash`",
	},
	MsgCurrentTools: {
		LangEnglish:            "Pre-allowed tools: %s",
		LangChinese:            "预授权的工具: %s",
		LangTraditionalChinese: "預授權的工具: %s",
		LangJapanese:           "事前許可済みツール: %s",
		LangSpanish:            "Herramientas pre-autorizadas: %s",
	},
	MsgCurrentSession: {
		LangEnglish:            "📌 Current session\nName: %s\nSession ID: %s\nLocal messages: %d",
		LangChinese:            "📌 当前会话\n名称: %s\n会话 ID: %s\n本地消息数: %d",
		LangTraditionalChinese: "📌 目前工作階段\n名稱: %s\n工作階段 ID: %s\n本機訊息數: %d",
		LangJapanese:           "📌 現在のセッション\n名前: %s\nセッション ID: %s\nローカルメッセージ数: %d",
		LangSpanish:            "📌 Sesión actual\nNombre: %s\nID de sesión: %s\nMensajes locales: %d",
	},
	MsgToolAuthNotSupported: {
		LangEnglish:            "This agent does not support tool authorization.",
		LangChinese:            "此代理不支持工具授权。",
		LangTraditionalChinese: "此代理不支援工具授權。",
		LangJapanese:           "このエージェントはツール認可をサポートしていません。",
		LangSpanish:            "Este agente no soporta la autorización de herramientas.",
	},
	MsgToolAllowFailed: {
		LangEnglish:            "Failed to allow tool: %v",
		LangChinese:            "授权工具失败: %v",
		LangTraditionalChinese: "授權工具失敗: %v",
		LangJapanese:           "ツール許可に失敗しました: %v",
		LangSpanish:            "Error al autorizar herramienta: %v",
	},
	MsgToolAllowedNew: {
		LangEnglish:            "✅ Tool `%s` pre-allowed. Takes effect on next session.",
		LangChinese:            "✅ 工具 `%s` 已预授权。将在下次会话生效。",
		LangTraditionalChinese: "✅ 工具 `%s` 已預授權。將在下次會話生效。",
		LangJapanese:           "✅ ツール `%s` を事前許可しました。次のセッションから有効になります。",
		LangSpanish:            "✅ Herramienta `%s` pre-autorizada. Se aplicará en la próxima sesión.",
	},
	MsgError: {
		LangEnglish:            "❌ Error: %v",
		LangChinese:            "❌ 错误: %v",
		LangTraditionalChinese: "❌ 錯誤: %v",
		LangJapanese:           "❌ エラー: %v",
		LangSpanish:            "❌ Error: %v",
	},
	MsgSessionNotFound: {
		LangEnglish:            "⚠️ Session expired. Use /new to start a fresh conversation.",
		LangChinese:            "⚠️ 会话已过期，请发送 /new 开始新会话",
		LangTraditionalChinese: "⚠️ 會話已過期，請發送 /new 開始新會話",
		LangJapanese:           "⚠️ セッションが期限切れです。/new で新しい会話を開始してください。",
		LangSpanish:            "⚠️ Sesión expirada. Usa /new para iniciar una nueva conversación.",
	},
	MsgFailedToStartAgentSession: {
		LangEnglish:            "❌ Error: failed to start agent session",
		LangChinese:            "❌ 错误: 启动 Agent 会话失败",
		LangTraditionalChinese: "❌ 錯誤: 啟動 Agent 會話失敗",
		LangJapanese:           "❌ エラー: Agentセッションの起動に失敗しました",
		LangSpanish:            "❌ Error: error al iniciar la sesión del agente",
	},
	MsgFailedToDeleteSession: {
		LangEnglish:            "❌ %s: %v",
		LangChinese:            "❌ %s: %v",
		LangTraditionalChinese: "❌ %s: %v",
		LangJapanese:           "❌ %s: %v",
		LangSpanish:            "❌ %s: %v",
	},
	MsgEmptyResponse: {
		LangEnglish:            "(empty response)",
		LangChinese:            "(空响应)",
		LangTraditionalChinese: "(空回應)",
		LangJapanese:           "（空のレスポンス）",
		LangSpanish:            "(respuesta vacía)",
	},
	MsgPermissionPrompt: {
		LangEnglish:            "⚠️ **Permission Request**\n\nAgent wants to use **%s**:\n\n```\n%s\n```\n\nReply **allow** / **deny** / **allow all** (skip all future prompts this session).",
		LangChinese:            "⚠️ **权限请求**\n\nAgent 想要使用 **%s**:\n\n```\n%s\n```\n\n回复 **允许** / **拒绝** / **允许所有**（本次会话不再提醒）。",
		LangTraditionalChinese: "⚠️ **權限請求**\n\nAgent 想要使用 **%s**:\n\n```\n%s\n```\n\n回覆 **允許** / **拒絕** / **允許所有**（本次會話不再提醒）。",
		LangJapanese:           "⚠️ **権限リクエスト**\n\nエージェントが **%s** を使用しようとしています:\n\n```\n%s\n```\n\n**allow** / **deny** / **allow all**（このセッション中は全て自動許可）で返信してください。",
		LangSpanish:            "⚠️ **Solicitud de permiso**\n\nEl agente quiere usar **%s**:\n\n```\n%s\n```\n\nResponda **allow** / **deny** / **allow all** (omitir futuras solicitudes en esta sesión).",
	},
	MsgPermissionAllowed: {
		LangEnglish:            "✅ Allowed, continuing...",
		LangChinese:            "✅ 已允许，继续执行...",
		LangTraditionalChinese: "✅ 已允許，繼續執行...",
		LangJapanese:           "✅ 許可しました。続行中...",
		LangSpanish:            "✅ Permitido, continuando...",
	},
	MsgPermissionApproveAll: {
		LangEnglish:            "✅ All permissions auto-approved for this session.",
		LangChinese:            "✅ 本次会话已开启自动批准，后续权限请求将自动允许。",
		LangTraditionalChinese: "✅ 本次會話已開啟自動批准，後續權限請求將自動允許。",
		LangJapanese:           "✅ このセッションの全ての権限を自動承認に設定しました。",
		LangSpanish:            "✅ Todos los permisos se aprobarán automáticamente en esta sesión.",
	},
	MsgPermissionDenied: {
		LangEnglish:            "❌ Denied. Agent will stop this tool use.",
		LangChinese:            "❌ 已拒绝。Agent 将停止此工具使用。",
		LangTraditionalChinese: "❌ 已拒絕。Agent 將停止此工具使用。",
		LangJapanese:           "❌ 拒否しました。エージェントはこのツールの使用を中止します。",
		LangSpanish:            "❌ Denegado. El agente detendrá el uso de esta herramienta.",
	},
	MsgPermissionHint: {
		LangEnglish:            "⚠️ Waiting for permission response. Reply **allow** / **deny** / **allow all**.",
		LangChinese:            "⚠️ 等待权限响应。请回复 **允许** / **拒绝** / **允许所有**。",
		LangTraditionalChinese: "⚠️ 等待權限回應。請回覆 **允許** / **拒絕** / **允許所有**。",
		LangJapanese:           "⚠️ 権限の応答を待っています。**allow** / **deny** / **allow all** で返信してください。",
		LangSpanish:            "⚠️ Esperando respuesta de permiso. Responda **allow** / **deny** / **allow all**.",
	},
	MsgPermissionNotRequester: {
		LangEnglish:            "🔒 Only the user who started this task or an admin can answer this permission request.",
		LangChinese:            "🔒 只有发起此任务的用户或管理员可以回应这个权限请求。",
		LangTraditionalChinese: "🔒 只有發起此任務的使用者或管理員可以回應這個權限請求。",
		LangJapanese:           "🔒 この権限リクエストに応答できるのは、このタスクを開始したユーザーまたは管理者だけです。",
		LangSpanish:            "🔒 Solo el usuario que inició esta tarea o un administrador puede responder a esta solicitud de permiso.",
	},
	MsgQuietOn: {
		LangEnglish:            "🔇 Quiet mode ON — thinking and tool progress messages will be hidden.",
		LangChinese:            "🔇 安静模式已开启 — 将不再推送思考和工具调用进度消息。",
		LangTraditionalChinese: "🔇 安靜模式已開啟 — 將不再推送思考和工具調用進度訊息。",
		LangJapanese:           "🔇 静音モード ON — 思考とツール実行の進捗メッセージを非表示にします。",
		LangSpanish:            "🔇 Modo silencioso activado — los mensajes de progreso se ocultarán.",
	},
	MsgQuietOff: {
		LangEnglish:            "🔔 Quiet mode OFF — thinking and tool progress messages will be shown.",
		LangChinese:            "🔔 安静模式已关闭 — 将恢复推送思考和工具调用进度消息。",
		LangTraditionalChinese: "🔔 安靜模式已關閉 — 將恢復推送思考和工具調用進度訊息。",
		LangJapanese:           "🔔 静音モード OFF — 思考とツール実行の進捗メッセージを表示します。",
		LangSpanish:            "🔔 Modo silencioso desactivado — los mensajes de progreso se mostrarán.",
	},
	MsgDisplayModeCompact: {
		LangEnglish:            "📋 Compact mode — thinking/tool hidden, each text segment sent separately.",
		LangChinese:            "📋 紧凑模式 — 隐藏思考和工具消息，每段文本独立发送。",
		LangTraditionalChinese: "📋 緊湊模式 — 隱藏思考和工具訊息，每段文字獨立發送。",
		LangJapanese:           "📋 コンパクトモード — 思考・ツール非表示、テキストは個別に送信。",
		LangSpanish:            "📋 Modo compacto — pensamiento/herramientas ocultos, cada segmento de texto enviado por separado.",
	},
	MsgQuietGlobalOn: {
		LangEnglish:            "🔇 Global quiet mode ON — all sessions will hide thinking and tool progress.",
		LangChinese:            "🔇 全局安静模式已开启 — 所有会话将不再推送思考和工具调用进度消息。",
		LangTraditionalChinese: "🔇 全域安靜模式已開啟 — 所有會話將不再推送思考和工具調用進度訊息。",
		LangJapanese:           "🔇 グローバル静音モード ON — 全セッションで思考とツール進捗を非表示にします。",
		LangSpanish:            "🔇 Modo silencioso global activado — todas las sesiones ocultarán los mensajes de progreso.",
	},
	MsgQuietGlobalOff: {
		LangEnglish:            "🔔 Global quiet mode OFF — all sessions will show thinking and tool progress.",
		LangChinese:            "🔔 全局安静模式已关闭 — 所有会话将恢复推送思考和工具调用进度消息。",
		LangTraditionalChinese: "🔔 全域安靜模式已關閉 — 所有會話將恢復推送思考和工具調用進度訊息。",
		LangJapanese:           "🔔 グローバル静音モード OFF — 全セッションで思考とツール進捗を表示します。",
		LangSpanish:            "🔔 Modo silencioso global desactivado — todas las sesiones mostrarán los mensajes de progreso.",
	},
	MsgModeInvalid: {
		LangEnglish:            "Unsupported permission mode: `%s`.",
		LangChinese:            "不支持的权限模式：`%s`。",
		LangTraditionalChinese: "不支援的權限模式：`%s`。",
		LangJapanese:           "未対応の権限モード: `%s`。",
		LangSpanish:            "Modo de permisos no admitido: `%s`.",
	},
	MsgPermissionDefaultName: {
		LangEnglish:            "Default permissions",
		LangChinese:            "默认权限",
		LangTraditionalChinese: "預設權限",
		LangJapanese:           "デフォルトの権限",
		LangSpanish:            "Permisos predeterminados",
	},
	MsgPermissionDefaultDesc: {
		LangEnglish:            "Work inside the workspace sandbox; ask you to approve additional access.",
		LangChinese:            "在工作区沙箱内操作，需要额外权限时由你审批。",
		LangTraditionalChinese: "在工作區沙箱內操作，需要額外權限時由你審批。",
		LangJapanese:           "ワークスペースのサンドボックス内で作業し、追加のアクセスはユーザーが承認します。",
		LangSpanish:            "Trabaja dentro del entorno aislado; tú apruebas el acceso adicional.",
	},
	MsgPermissionAutoReviewName: {
		LangEnglish:            "Auto-review",
		LangChinese:            "自动审核",
		LangTraditionalChinese: "自動審核",
		LangJapanese:           "自動レビュー",
		LangSpanish:            "Revisión automática",
	},
	MsgPermissionAutoReviewDesc: {
		LangEnglish:            "Keep the workspace sandbox; automatically review permission requests, which may be approved or denied.",
		LangChinese:            "保留工作区沙箱，自动审核权限请求，可以允许或拒绝。",
		LangTraditionalChinese: "保留工作區沙箱，自動審核權限請求，可以允許或拒絕。",
		LangJapanese:           "サンドボックスを維持し、権限リクエストを自動で承認または拒否します。",
		LangSpanish:            "Mantiene el entorno aislado y revisa automáticamente las solicitudes para aprobarlas o rechazarlas.",
	},
	MsgPermissionReadOnlyName: {
		LangEnglish:            "Read-only",
		LangChinese:            "只读",
		LangTraditionalChinese: "唯讀",
		LangJapanese:           "読み取り専用",
		LangSpanish:            "Solo lectura",
	},
	MsgPermissionReadOnlyDesc: {
		LangEnglish:            "Read-only sandbox; ask you to approve operations outside that boundary.",
		LangChinese:            "只读沙箱，超出只读权限的操作由你审批。",
		LangTraditionalChinese: "唯讀沙箱，超出唯讀權限的操作由你審批。",
		LangJapanese:           "読み取り専用サンドボックスの範囲を超える操作はユーザーが承認します。",
		LangSpanish:            "Entorno de solo lectura; tú apruebas las operaciones que excedan ese límite.",
	},
	MsgPermissionReadOnlyExecDesc: {
		LangEnglish:            "Read-only sandbox; operations requiring approval fail because this backend cannot request it.",
		LangChinese:            "只读沙箱；此后端无法申请审批，需要额外权限的操作会失败。",
		LangTraditionalChinese: "唯讀沙箱；此後端無法申請審批，需要額外權限的操作會失敗。",
		LangJapanese:           "読み取り専用です。このバックエンドは承認を要求できないため、追加権限が必要な操作は失敗します。",
		LangSpanish:            "Solo lectura; este motor no puede solicitar aprobación y las operaciones que la requieran fallan.",
	},
	MsgPermissionFullAccessName: {
		LangEnglish:            "Full access",
		LangChinese:            "完全访问权限",
		LangTraditionalChinese: "完整存取權限",
		LangJapanese:           "フルアクセス",
		LangSpanish:            "Acceso completo",
	},
	MsgPermissionFullAccessDesc: {
		LangEnglish:            "Access the computer without sandbox restrictions or approval prompts.",
		LangChinese:            "不受沙箱限制访问计算机，不请求审批。",
		LangTraditionalChinese: "不受沙箱限制存取電腦，不請求審批。",
		LangJapanese:           "サンドボックスの制限や承認要求なしでコンピューターにアクセスします。",
		LangSpanish:            "Accede al equipo sin restricciones del entorno aislado ni solicitudes de aprobación.",
	},
	MsgModeChanged: {
		LangEnglish:            "🔄 Permission mode switched to **%s**. New sessions will use this mode.",
		LangChinese:            "🔄 权限模式已切换为 **%s**，新会话将使用此模式。",
		LangTraditionalChinese: "🔄 權限模式已切換為 **%s**，新會話將使用此模式。",
		LangJapanese:           "🔄 権限モードを **%s** に切り替えました。新しいセッションで有効になります。",
		LangSpanish:            "🔄 Modo de permisos cambiado a **%s**. Las nuevas sesiones usarán este modo.",
	},
	MsgModeNotSupported: {
		LangEnglish:            "This agent does not support permission mode switching.",
		LangChinese:            "当前 Agent 不支持权限模式切换。",
		LangTraditionalChinese: "當前 Agent 不支援權限模式切換。",
		LangJapanese:           "このエージェントは権限モードの切り替えをサポートしていません。",
		LangSpanish:            "Este agente no soporta el cambio de modo de permisos.",
	},
	MsgSessionRestarting: {
		LangEnglish:            "🔄 Session process exited, restarting...",
		LangChinese:            "🔄 会话进程已退出，正在重启...",
		LangTraditionalChinese: "🔄 會話進程已退出，正在重啟...",
		LangJapanese:           "🔄 セッションプロセスが終了しました。再起動中...",
		LangSpanish:            "🔄 El proceso de sesión finalizó, reiniciando...",
	},
	MsgAgentExitedMidTurn: {
		LangEnglish:            "⚠️ The agent process exited unexpectedly, so this reply is incomplete. Resend your message or ask it to continue.",
		LangChinese:            "⚠️ Agent 进程意外退出，本轮回复没有完成。可以重新发送，或让它继续。",
		LangTraditionalChinese: "⚠️ Agent 進程意外退出，本輪回覆沒有完成。可以重新傳送，或讓它繼續。",
		LangJapanese:           "⚠️ エージェントのプロセスが予期せず終了したため、この返信は完了していません。再送するか、続きを依頼してください。",
		LangSpanish:            "⚠️ El proceso del agente terminó inesperadamente, así que esta respuesta está incompleta. Reenvía tu mensaje o pide que continúe.",
	},
	MsgTurnInterrupted: {
		LangEnglish:            "⚠️ The service stopped (restart or crash) before finishing the reply to the message received at %s. Resend it or ask it to continue.",
		LangChinese:            "⚠️ %s 收到的消息还没回复完，服务就中断了（重启或异常退出）。可以重新发送，或让它继续。",
		LangTraditionalChinese: "⚠️ %s 收到的訊息還沒回覆完，服務就中斷了（重啟或異常退出）。可以重新傳送，或讓它繼續。",
		LangJapanese:           "⚠️ %s に受信したメッセージへの返信が終わる前に、サービスが停止しました（再起動または異常終了）。再送するか、続きを依頼してください。",
		LangSpanish:            "⚠️ El servicio se detuvo (reinicio o fallo) antes de terminar la respuesta al mensaje recibido a las %s. Reenvíalo o pide que continúe.",
	},
	MsgTimerInterrupted: {
		LangEnglish:            "⚠️ The service stopped (restart or crash) before the timer task that started at %s had finished. It will not run again; schedule it again if you still need it.",
		LangChinese:            "⚠️ %s 开始执行的定时任务还没做完，服务就中断了（重启或异常退出）。它不会再自动执行，需要的话重新安排。",
		LangTraditionalChinese: "⚠️ %s 開始執行的定時任務還沒做完，服務就中斷了（重啟或異常退出）。它不會再自動執行，需要的話重新安排。",
		LangJapanese:           "⚠️ %s に開始したタイマータスクが終わる前に、サービスが停止しました（再起動または異常終了）。自動では再実行されません。必要なら設定し直してください。",
		LangSpanish:            "⚠️ El servicio se detuvo (reinicio o fallo) antes de que terminara la tarea programada iniciada a las %s. No se volverá a ejecutar; prográmala de nuevo si aún la necesitas.",
	},
	MsgStallModel: {
		LangEnglish:            "⏳ No new output for %d minutes while waiting on the model, possibly a network or API problem. If it is stuck, send /stop to end it.",
		LangChinese:            "⏳ 已经 %d 分钟没有新输出，在等模型返回，可能是网络或接口问题。如果卡住了，发 /stop 中止。",
		LangTraditionalChinese: "⏳ 已經 %d 分鐘沒有新輸出，在等模型回傳，可能是網路或介面問題。如果卡住了，傳送 /stop 中止。",
		LangJapanese:           "⏳ モデルの応答待ちのまま %d 分間新しい出力がありません。ネットワークまたは API の問題かもしれません。止まっている場合は /stop で中止してください。",
		LangSpanish:            "⏳ Sin salida nueva durante %d minutos esperando al modelo; puede ser un problema de red o de la API. Si está atascado, envía /stop para detenerlo.",
	},
	MsgStallTool: {
		LangEnglish:            "⏳ %s has been running for %d minutes with no new output. If it is stuck, send /stop to end it.",
		LangChinese:            "⏳ 正在执行 %s，已经 %d 分钟没有新输出。如果卡住了，发 /stop 中止。",
		LangTraditionalChinese: "⏳ 正在執行 %s，已經 %d 分鐘沒有新輸出。如果卡住了，傳送 /stop 中止。",
		LangJapanese:           "⏳ %s を実行中で、%d 分間新しい出力がありません。止まっている場合は /stop で中止してください。",
		LangSpanish:            "⏳ %s lleva %d minutos ejecutándose sin salida nueva. Si está atascado, envía /stop para detenerlo.",
	},
	MsgRetryNotice: {
		LangEnglish:            "⚠️ The model request failed and is being retried automatically (attempt %s, reason: %s%s). If every retry fails you will get an error; send /stop to give up now.",
		LangChinese:            "⚠️ 模型接口请求失败，正在自动重试（第 %s 次，原因：%s%s）。全部重试失败会返回错误；不想等可以发 /stop 中止。",
		LangTraditionalChinese: "⚠️ 模型介面請求失敗，正在自動重試（第 %s 次，原因：%s%s）。全部重試失敗會回傳錯誤；不想等可以傳送 /stop 中止。",
		LangJapanese:           "⚠️ モデルへのリクエストが失敗したため自動で再試行しています（%s 回目、原因：%s%s）。すべて失敗した場合はエラーが返ります。待たない場合は /stop で中止してください。",
		LangSpanish:            "⚠️ La solicitud al modelo falló y se está reintentando automáticamente (intento %s, motivo: %s%s). Si todos los reintentos fallan recibirás un error; envía /stop para abandonar ahora.",
	},
	MsgRetryNextIn: {
		LangEnglish:            ", next try in %s",
		LangChinese:            "，%s后再试",
		LangTraditionalChinese: "，%s後再試",
		LangJapanese:           "、%s後に再試行",
		LangSpanish:            ", próximo intento en %s",
	},
	MsgRetryReasonRateLimit: {
		LangEnglish:            "rate limited",
		LangChinese:            "触发限流",
		LangTraditionalChinese: "觸發限流",
		LangJapanese:           "レート制限",
		LangSpanish:            "límite de uso alcanzado",
	},
	MsgRetryReasonOverloaded: {
		LangEnglish:            "service overloaded",
		LangChinese:            "服务过载",
		LangTraditionalChinese: "服務過載",
		LangJapanese:           "サービス過負荷",
		LangSpanish:            "servicio sobrecargado",
	},
	MsgRetryReasonAuth: {
		LangEnglish:            "authentication failed",
		LangChinese:            "认证失败",
		LangTraditionalChinese: "認證失敗",
		LangJapanese:           "認証失敗",
		LangSpanish:            "autenticación fallida",
	},
	MsgRetryReasonServer: {
		LangEnglish:            "server error",
		LangChinese:            "服务端错误",
		LangTraditionalChinese: "服務端錯誤",
		LangJapanese:           "サーバーエラー",
		LangSpanish:            "error del servidor",
	},
	MsgRetryReasonNoResponse: {
		LangEnglish:            "no response for a long time",
		LangChinese:            "长时间没有响应",
		LangTraditionalChinese: "長時間沒有回應",
		LangJapanese:           "長時間応答なし",
		LangSpanish:            "sin respuesta durante mucho tiempo",
	},
	MsgRetryReasonNetwork: {
		LangEnglish:            "network or unknown error",
		LangChinese:            "网络或未知错误",
		LangTraditionalChinese: "網路或未知錯誤",
		LangJapanese:           "ネットワークまたは不明なエラー",
		LangSpanish:            "error de red o desconocido",
	},
	MsgSessionNotStarted: {
		LangEnglish:            "(new — not yet started)",
		LangChinese:            "(新会话 — 尚未开始)",
		LangTraditionalChinese: "(新會話 — 尚未開始)",
		LangJapanese:           "(新規 — まだ開始されていません)",
		LangSpanish:            "(nuevo — aún no iniciado)",
	},
	MsgUntitled: {
		LangEnglish:            "(untitled)",
		LangChinese:            "(未命名)",
		LangTraditionalChinese: "(未命名)",
		LangJapanese:           "(無題)",
		LangSpanish:            "(sin título)",
	},
	MsgLangChanged: {
		LangEnglish:            "🌐 Language switched to **%s**.",
		LangChinese:            "🌐 语言已切换为 **%s**。",
		LangTraditionalChinese: "🌐 語言已切換為 **%s**。",
		LangJapanese:           "🌐 言語を **%s** に切り替えました。",
		LangSpanish:            "🌐 Idioma cambiado a **%s**.",
	},
	MsgLangInvalid: {
		LangEnglish:            "Unknown language. Supported: `en`, `zh`, `zh-TW`, `ja`, `es`, `auto`.",
		LangChinese:            "未知语言。支持: `en`, `zh`, `zh-TW`, `ja`, `es`, `auto`。",
		LangTraditionalChinese: "未知語言。支援: `en`, `zh`, `zh-TW`, `ja`, `es`, `auto`。",
		LangJapanese:           "不明な言語です。対応: `en`, `zh`, `zh-TW`, `ja`, `es`, `auto`。",
		LangSpanish:            "Idioma desconocido. Soportados: `en`, `zh`, `zh-TW`, `ja`, `es`, `auto`.",
	},
	MsgLangCurrent: {
		LangEnglish:            "🌐 Current language: **%s**\n\nUsage: /lang <en|zh|zh-TW|ja|es|auto>",
		LangChinese:            "🌐 当前语言: **%s**\n\n用法: /lang <en|zh|zh-TW|ja|es|auto>",
		LangTraditionalChinese: "🌐 當前語言: **%s**\n\n用法: /lang <en|zh|zh-TW|ja|es|auto>",
		LangJapanese:           "🌐 現在の言語: **%s**\n\n使い方: /lang <en|zh|zh-TW|ja|es|auto>",
		LangSpanish:            "🌐 Idioma actual: **%s**\n\nUso: /lang <en|zh|zh-TW|ja|es|auto>",
	},
	MsgUnknownCommand: {
		LangEnglish:            "`%s` is not a lark-agent-bot command, forwarding to agent...",
		LangChinese:            "`%s` 不是 lark-agent-bot 命令，已转发给 Agent 处理...",
		LangTraditionalChinese: "`%s` 不是 lark-agent-bot 命令，已轉發給 Agent 處理...",
		LangJapanese:           "`%s` は lark-agent-bot のコマンドではありません。エージェントに転送します...",
		LangSpanish:            "`%s` no es un comando de lark-agent-bot, reenviando al agente...",
	},
	MsgWelcome: {
		LangEnglish:            "👋 Hi! I'm lark-agent-bot, bridging you to **%s**.\n\nJust send a message to chat with the agent. Type /help to see built-in commands.",
		LangChinese:            "👋 你好！我是 lark-agent-bot，已为你连接到 **%s**。\n\n直接发送消息即可与 Agent 对话。输入 /help 查看内置命令。",
		LangTraditionalChinese: "👋 你好！我是 lark-agent-bot，已為你連接到 **%s**。\n\n直接發送訊息即可與 Agent 對話。輸入 /help 查看內建命令。",
		LangJapanese:           "👋 こんにちは！lark-agent-bot が **%s** に接続しました。\n\nメッセージを送信すればエージェントと会話できます。/help で組み込みコマンド一覧を確認できます。",
		LangSpanish:            "👋 ¡Hola! Soy lark-agent-bot, conectándote con **%s**.\n\nEnvía un mensaje para chatear con el agente. Usa /help para ver los comandos integrados.",
	},
	MsgHelp: {
		LangEnglish: "📖 Available Commands\n\n" +
			"/new [name]\n  Start a new session\n\n" +
			"/list\n  List agent sessions\n\n" +
			"/search <keyword>\n  Search sessions by name or ID\n\n" +
			"/switch <number>\n  Resume a session by its list number\n\n" +
			"/delete <number>|1,2,3|3-7|1,3-5,8\n  Delete sessions by list number(s)\n\n" +
			"/name [number] <text>\n  Name a session for easy identification\n\n" +
			"/current\n  Show current active session\n\n" +
			"/history [n]\n  Show last n messages (default 10)\n\n" +
			"/provider [list|add|remove|switch|clear]\n  Manage API providers\n\n" +
			"/memory [add|global|global add]\n  View/edit agent memory files\n\n" +
			"/allow <tool>\n  Pre-allow a tool (next session)\n\n" +
			"/model [switch <name>]\n  View/switch model\n\n" +
			"/reasoning [level]\n  View/switch reasoning effort\n\n" +
			"/mode [name]\n  View/switch permission mode\n\n" +
			"/lang [en|zh|zh-TW|ja|es|auto]\n  View/switch language\n\n" +
			"/compress\n  Compress conversation context\n\n" +
			"/tts [always|voice_only]\n  View/switch text-to-speech mode\n\n" +
			"/shell [--timeout <sec>] <command>\n  Run a shell command and return the output (! prefix shortcut: !cmd)\n\n" +
			"/show <ref>\n  View a file, directory, or code snippet by reference\n\n" +
			"/dir [path|reset]\n  Show, switch, or reset agent working directory\n\n" +
			"/stop\n  Stop current execution\n\n" +
			"/cron [add|list|exec|del|enable|disable]\n  Manage scheduled tasks\n\n" +
			"/timer [add|list|del|mute|unmute]\n  Manage one-shot timers\n\n" +
			"/heartbeat [status|pause|resume|run|interval]\n  Manage heartbeat\n\n" +
			"/commands [add|del]\n  Manage custom slash commands\n\n" +
			"/alias [add|del]\n  Manage command aliases (e.g. 帮助 → /help)\n\n" +
			"/skills\n  List agent skills (from SKILL.md)\n\n" +
			"/config [get|set|reload] [key] [value]\n  View/update runtime configuration\n\n" +
			"/bind [project|remove]\n  Manage relay binding in group chats\n\n" +
			"/workspace [init]\n  Manage workspace\n\n" +
			"/doctor\n  Run system diagnostics\n\n" +
			"/usage\n  Show account/model quota usage\n\n" +
			"/upgrade\n  Check for updates and self-update\n\n" +
			"/restart\n  Restart lark-agent-bot service\n\n" +
			"/status\n  Show system status\n\n" +
			"/version\n  Show lark-agent-bot version\n\n" +
			"/whoami\n  Show your User ID (for allow_from / admin_from)\n\n" +
			"/help\n  Show this help\n\n" +
			"Tip: Commands support prefix matching, e.g. `/pro l` = `/provider list`, `/sw 2` = `/switch 2`.\n\n" +
			"Custom commands: define via `/commands add` or `[[commands]]` in config.toml.\n\n" +
			"Command aliases: use `/alias add <trigger> <command>` or `[[aliases]]` in config.toml.\n\n" +
			"Agent skills: auto-discovered from .claude/skills/<name>/SKILL.md etc.\n\n" +
			"Permission modes: default / edit / plan / yolo",
		LangChinese: "📖 可用命令\n\n" +
			"/new [名称]\n  创建新会话\n\n" +
			"/list\n  列出 Agent 会话列表\n\n" +
			"/search <关键词>\n  搜索会话名称或 ID\n\n" +
			"/switch <序号>\n  按列表序号切换会话\n\n" +
			"/delete <序号>|1,2,3|3-7|1,3-5,8\n  按列表序号批量/单个删除会话\n\n" +
			"/name [序号] <名称>\n  给会话命名，方便识别\n\n" +
			"/current\n  查看当前活跃会话\n\n" +
			"/history [n]\n  查看最近 n 条消息（默认 10）\n\n" +
			"/provider [list|add|remove|switch|clear]\n  管理 API Provider\n\n" +
			"/memory [add|global|global add]\n  查看/编辑 Agent 记忆文件\n\n" +
			"/allow <工具名>\n  预授权工具（下次会话生效）\n\n" +
			"/model [switch <名称>]\n  查看/切换模型\n\n" +
			"/reasoning [级别]\n  查看/切换推理强度\n\n" +
			"/mode [名称]\n  查看/切换权限模式\n\n" +
			"/lang [en|zh|zh-TW|ja|es|auto]\n  查看/切换语言\n\n" +
			"/compress\n  压缩会话上下文\n\n" +
			"/tts [always|voice_only]\n  查看/切换语音合成模式\n\n" +
			"/shell [--timeout <秒>] <命令>\n  执行 Shell 命令并返回结果（快捷方式：!命令）\n\n" +
			"/show <引用>\n  按引用查看文件、目录或代码片段\n\n" +
			"/dir [路径|reset]\n  查看、切换或重置 Agent 工作目录\n\n" +
			"/stop\n  停止当前执行\n\n" +
			"/cron [add|list|exec|del|enable|disable]\n  管理定时任务\n\n" +
			"/timer [add|list|del|mute|unmute]\n  管理一次性定时器\n\n" +
			"/heartbeat [status|pause|resume|run|interval]\n  管理心跳\n\n" +
			"/commands [add|del]\n  管理自定义命令\n\n" +
			"/alias [add|del]\n  管理命令别名（如 帮助 → /help）\n\n" +
			"/skills\n  列出 Agent Skills（来自 SKILL.md）\n\n" +
			"/config [get|set|reload] [key] [value]\n  查看/修改运行时配置\n\n" +
			"/bind [项目名|remove]\n  管理群聊中继绑定\n\n" +
			"/workspace [init]\n  管理工作区\n\n" +
			"/doctor\n  运行系统诊断\n\n" +
			"/usage\n  查看账号/模型限额使用情况\n\n" +
			"/upgrade\n  检查更新并自动升级\n\n" +
			"/restart\n  重启 lark-agent-bot 服务\n\n" +
			"/status\n  查看系统状态\n\n" +
			"/version\n  查看 lark-agent-bot 版本\n\n" +
			"/whoami\n  查看你的 User ID（用于 allow_from / admin_from 配置）\n\n" +
			"/help\n  显示此帮助\n\n" +
			"提示：命令支持前缀匹配，如 `/pro l` = `/provider list`，`/sw 2` = `/switch 2`。\n\n" +
			"自定义命令：通过 `/commands add` 添加，或在 config.toml 中配置 `[[commands]]`。\n\n" +
			"命令别名：使用 `/alias add <触发词> <命令>` 或在 config.toml 中配置 `[[aliases]]`。\n\n" +
			"Agent Skills：自动发现自 .claude/skills/<name>/SKILL.md 等目录。\n\n" +
			"权限模式：default / edit / plan / yolo",
		LangTraditionalChinese: "📖 可用命令\n\n" +
			"/new [名稱]\n  建立新會話\n\n" +
			"/list\n  列出 Agent 會話列表\n\n" +
			"/search <關鍵詞>\n  搜尋會話名稱或 ID\n\n" +
			"/switch <序號>\n  按列表序號切換會話\n\n" +
			"/delete <序號>|1,2,3|3-7|1,3-5,8\n  按列表序號批量/單筆刪除會話\n\n" +
			"/name [序號] <名稱>\n  為會話命名，方便辨識\n\n" +
			"/current\n  查看當前活躍會話\n\n" +
			"/history [n]\n  查看最近 n 條訊息（預設 10）\n\n" +
			"/provider [list|add|remove|switch|clear]\n  管理 API Provider\n\n" +
			"/memory [add|global|global add]\n  查看/編輯 Agent 記憶檔案\n\n" +
			"/allow <工具名>\n  預授權工具（下次會話生效）\n\n" +
			"/model [switch <名稱>]\n  查看/切換模型\n\n" +
			"/reasoning [級別]\n  查看/切換推理強度\n\n" +
			"/mode [名稱]\n  查看/切換權限模式\n\n" +
			"/lang [en|zh|zh-TW|ja|es|auto]\n  查看/切換語言\n\n" +
			"/compress\n  壓縮會話上下文\n\n" +
			"/tts [always|voice_only]\n  查看/切換語音合成模式\n\n" +
			"/shell [--timeout <秒>] <命令>\n  執行 Shell 命令並返回結果（快捷方式：!命令）\n\n" +
			"/dir [路徑|reset]\n  查看、切換或重置 Agent 工作目錄\n\n" +
			"/stop\n  停止當前執行\n\n" +
			"/cron [add|list|exec|del|enable|disable]\n  管理定時任務\n\n" +
			"/timer [add|list|del|mute|unmute]\n  管理一次性定時器\n\n" +
			"/heartbeat [status|pause|resume|run|interval]\n  管理心跳\n\n" +
			"/commands [add|del]\n  管理自訂命令\n\n" +
			"/alias [add|del]\n  管理命令別名（如 幫助 → /help）\n\n" +
			"/skills\n  列出 Agent Skills（來自 SKILL.md）\n\n" +
			"/config [get|set|reload] [key] [value]\n  查看/修改執行階段配置\n\n" +
			"/bind [項目名|remove]\n  管理群聊中繼綁定\n\n" +
			"/workspace [init]\n  管理工作區\n\n" +
			"/doctor\n  執行系統診斷\n\n" +
			"/usage\n  查看帳號/模型限額使用情況\n\n" +
			"/upgrade\n  檢查更新並自動升級\n\n" +
			"/restart\n  重啟 lark-agent-bot 服務\n\n" +
			"/status\n  查看系統狀態\n\n" +
			"/version\n  查看 lark-agent-bot 版本\n\n" +
			"/whoami\n  查看你的 User ID（用於 allow_from / admin_from 設定）\n\n" +
			"/help\n  顯示此說明\n\n" +
			"提示：命令支持前綴匹配，如 `/pro l` = `/provider list`，`/sw 2` = `/switch 2`。\n\n" +
			"自訂命令：透過 `/commands add` 新增，或在 config.toml 中配置 `[[commands]]`。\n\n" +
			"命令別名：使用 `/alias add <觸發詞> <命令>` 或在 config.toml 中配置 `[[aliases]]`。\n\n" +
			"Agent Skills：自動發現自 .claude/skills/<name>/SKILL.md 等目錄。\n\n" +
			"權限模式：default / edit / plan / yolo",
		LangJapanese: "📖 利用可能なコマンド\n\n" +
			"/new [名前]\n  新しいセッションを開始\n\n" +
			"/list\n  エージェントセッション一覧\n\n" +
			"/switch <番号>\n  リスト番号でセッションを切り替え\n\n" +
			"/delete <番号>|1,2,3|3-7|1,3-5,8\n  リスト番号でセッションを単体/複数削除\n\n" +
			"/name [番号] <名前>\n  セッションに名前を付ける\n\n" +
			"/current\n  現在のアクティブセッションを表示\n\n" +
			"/history [n]\n  直近 n 件のメッセージを表示（デフォルト 10）\n\n" +
			"/provider [list|add|remove|switch|clear]\n  API プロバイダ管理\n\n" +
			"/memory [add|global|global add]\n  エージェントメモリの表示/編集\n\n" +
			"/allow <ツール名>\n  ツールを事前許可（次のセッションで有効）\n\n" +
			"/model [switch <名前>]\n  モデルの表示/切り替え\n\n" +
			"/reasoning [レベル]\n  推論レベルの表示/切り替え\n\n" +
			"/mode [名前]\n  権限モードの表示/切り替え\n\n" +
			"/lang [en|zh|zh-TW|ja|es|auto]\n  言語の表示/切り替え\n\n" +
			"/compress\n  会話コンテキストを圧縮\n\n" +
			"/tts [always|voice_only]\n  音声合成モードの表示/切り替え\n\n" +
			"/shell [--timeout <秒>] <コマンド>\n  シェルコマンドを実行して結果を返す（ショートカット：!コマンド）\n\n" +
			"/dir [パス|reset]\n  エージェントの作業ディレクトリを表示/切り替え/リセット\n\n" +
			"/stop\n  現在の実行を停止\n\n" +
			"/cron [add|list|exec|del|enable|disable]\n  スケジュールタスク管理\n\n" +
			"/timer [add|list|del|mute|unmute]\n  ワンショットタイマー管理\n\n" +
			"/heartbeat [status|pause|resume|run|interval]\n  ハートビート管理\n\n" +
			"/commands [add|del]\n  カスタムコマンド管理\n\n" +
			"/alias [add|del]\n  コマンドエイリアス管理（例: ヘルプ → /help）\n\n" +
			"/skills\n  エージェントスキル一覧（SKILL.md から）\n\n" +
			"/config [get|set|reload] [key] [value]\n  ランタイム設定の表示/変更\n\n" +
			"/bind [プロジェクト|remove]\n  グループチャットのリレー管理\n\n" +
			"/workspace [init]\n  ワークスペース管理\n\n" +
			"/doctor\n  システム診断を実行\n\n" +
			"/usage\n  アカウント/モデル使用量を表示\n\n" +
			"/upgrade\n  アップデートを確認して自動更新\n\n" +
			"/restart\n  lark-agent-bot サービスを再起動\n\n" +
			"/status\n  システム状態を表示\n\n" +
			"/version\n  lark-agent-bot のバージョンを表示\n\n" +
			"/whoami\n  あなたの User ID を表示（allow_from / admin_from 設定用）\n\n" +
			"/help\n  このヘルプを表示\n\n" +
			"ヒント：コマンドはプレフィックスマッチに対応しています。例: `/pro l` = `/provider list`、`/sw 2` = `/switch 2`。\n\n" +
			"カスタムコマンド: `/commands add` または config.toml の `[[commands]]` で定義。\n\n" +
			"コマンドエイリアス: `/alias add <トリガー> <コマンド>` または config.toml の `[[aliases]]` で定義。\n\n" +
			"エージェントスキル: .claude/skills/<name>/SKILL.md などから自動検出。\n\n" +
			"権限モード: default / edit / plan / yolo",
		LangSpanish: "📖 Comandos disponibles\n\n" +
			"/new [nombre]\n  Iniciar una nueva sesión\n\n" +
			"/list\n  Listar sesiones del agente\n\n" +
			"/switch <número>\n  Reanudar sesión por su número en la lista\n\n" +
			"/delete <número>|1,2,3|3-7|1,3-5,8\n  Eliminar una o varias sesiones por número de lista\n\n" +
			"/name [número] <texto>\n  Nombrar una sesión para fácil identificación\n\n" +
			"/current\n  Mostrar sesión activa actual\n\n" +
			"/history [n]\n  Mostrar últimos n mensajes (por defecto 10)\n\n" +
			"/provider [list|add|remove|switch|clear]\n  Gestionar proveedores API\n\n" +
			"/memory [add|global|global add]\n  Ver/editar archivos de memoria del agente\n\n" +
			"/allow <herramienta>\n  Pre-autorizar herramienta (próxima sesión)\n\n" +
			"/model [switch <nombre>]\n  Ver/cambiar modelo\n\n" +
			"/reasoning [nivel]\n  Ver/cambiar nivel de razonamiento\n\n" +
			"/mode [nombre]\n  Ver/cambiar modo de permisos\n\n" +
			"/lang [en|zh|zh-TW|ja|es|auto]\n  Ver/cambiar idioma\n\n" +
			"/compress\n  Comprimir contexto de conversación\n\n" +
			"/tts [always|voice_only]\n  Ver/cambiar modo de síntesis de voz\n\n" +
			"/shell [--timeout <seg>] <comando>\n  Ejecutar un comando shell y devolver la salida (atajo: !comando)\n\n" +
			"/dir [ruta|reset]\n  Ver, cambiar o restablecer el directorio de trabajo del agente\n\n" +
			"/stop\n  Detener ejecución actual\n\n" +
			"/cron [add|list|exec|del|enable|disable]\n  Gestionar tareas programadas\n\n" +
			"/timer [add|list|del|mute|unmute]\n  Gestionar temporizadores de uso único\n\n" +
			"/heartbeat [status|pause|resume|run|interval]\n  Gestionar heartbeat\n\n" +
			"/commands [add|del]\n  Gestionar comandos personalizados\n\n" +
			"/alias [add|del]\n  Gestionar alias de comandos (ej. ayuda → /help)\n\n" +
			"/skills\n  Listar skills del agente (desde SKILL.md)\n\n" +
			"/config [get|set|reload] [key] [value]\n  Ver/actualizar configuración en tiempo de ejecución\n\n" +
			"/bind [proyecto|remove]\n  Gestionar retransmisión en chats de grupo\n\n" +
			"/workspace [init]\n  Gestionar workspace\n\n" +
			"/doctor\n  Ejecutar diagnósticos del sistema\n\n" +
			"/usage\n  Mostrar uso de cuota de cuenta/modelo\n\n" +
			"/upgrade\n  Buscar actualizaciones y auto-actualizar\n\n" +
			"/restart\n  Reiniciar el servicio lark-agent-bot\n\n" +
			"/status\n  Mostrar estado del sistema\n\n" +
			"/version\n  Mostrar versión de lark-agent-bot\n\n" +
			"/whoami\n  Mostrar tu User ID (para allow_from / admin_from)\n\n" +
			"/help\n  Mostrar esta ayuda\n\n" +
			"Consejo: Los comandos admiten coincidencia por prefijo, ej. `/pro l` = `/provider list`, `/sw 2` = `/switch 2`.\n\n" +
			"Comandos personalizados: use `/commands add` o defina `[[commands]]` en config.toml.\n\n" +
			"Alias de comandos: use `/alias add <trigger> <comando>` o `[[aliases]]` en config.toml.\n\n" +
			"Skills del agente: descubiertos de .claude/skills/<name>/SKILL.md etc.\n\n" +
			"Modos de permisos: default / edit / plan / yolo",
	},
	MsgHelpTitle: {
		LangEnglish:            "lark-agent-bot Help",
		LangChinese:            "lark-agent-bot 帮助",
		LangTraditionalChinese: "lark-agent-bot 說明",
		LangJapanese:           "lark-agent-bot ヘルプ",
		LangSpanish:            "lark-agent-bot Ayuda",
	},
	MsgHelpSessionSection: {
		LangEnglish: "**Session Management**\n" +
			"/new [name] — Start a new session\n" +
			"/list — List agent sessions\n" +
			"/search <keyword> — Search sessions\n" +
			"/switch <number> — Resume a session\n" +
			"/delete <number>|1,2,3|3-7|1,3-5,8 — Delete session(s)\n" +
			"/name [number] <text> — Name a session\n" +
			"/current — Show active session\n" +
			"/history [n] — Show last n messages",
		LangChinese: "**会话管理**\n" +
			"/new [名称] — 创建新会话\n" +
			"/list — 列出会话列表\n" +
			"/search <关键词> — 搜索会话\n" +
			"/switch <序号> — 切换会话\n" +
			"/delete <序号>|1,2,3|3-7|1,3-5,8 — 删除会话\n" +
			"/name [序号] <名称> — 命名会话\n" +
			"/current — 查看当前会话\n" +
			"/history [n] — 查看最近 n 条消息",
		LangTraditionalChinese: "**會話管理**\n" +
			"/new [名稱] — 建立新會話\n" +
			"/list — 列出會話列表\n" +
			"/search <關鍵詞> — 搜尋會話\n" +
			"/switch <序號> — 切換會話\n" +
			"/delete <序號>|1,2,3|3-7|1,3-5,8 — 刪除會話\n" +
			"/name [序號] <名稱> — 命名會話\n" +
			"/current — 查看當前會話\n" +
			"/history [n] — 查看最近 n 條訊息",
		LangJapanese: "**セッション管理**\n" +
			"/new [名前] — 新しいセッションを開始\n" +
			"/list — セッション一覧\n" +
			"/search <キーワード> — セッション検索\n" +
			"/switch <番号> — セッション切り替え\n" +
			"/delete <番号>|1,2,3|3-7|1,3-5,8 — セッション削除\n" +
			"/name [番号] <名前> — セッションに名前を付ける\n" +
			"/current — 現在のセッションを表示\n" +
			"/history [n] — 直近 n 件のメッセージを表示",
		LangSpanish: "**Gestión de sesiones**\n" +
			"/new [nombre] — Iniciar nueva sesión\n" +
			"/list — Listar sesiones\n" +
			"/search <keyword> — Buscar sesiones\n" +
			"/switch <número> — Reanudar sesión\n" +
			"/delete <número>|1,2,3|3-7|1,3-5,8 — Eliminar sesión(es)\n" +
			"/name [número] <texto> — Nombrar sesión\n" +
			"/current — Mostrar sesión activa\n" +
			"/history [n] — Mostrar últimos n mensajes",
	},
	MsgHelpAgentSection: {
		LangEnglish: "**Agent Configuration**\n" +
			"/model [switch <name>] — View/switch model\n" +
			"/mode [name] — View/switch permission mode\n" +
			"/provider [list|add|...] — Manage API providers\n" +
			"/memory [add|global|...] — View/edit memory files\n" +
			"/allow <tool> — Pre-allow a tool\n" +
			"/lang [en|zh|...] — View/switch language",
		LangChinese: "**Agent 配置**\n" +
			"/model [switch <名称>] — 查看/切换模型\n" +
			"/mode [名称] — 查看/切换权限模式\n" +
			"/provider [list|add|...] — 管理 API Provider\n" +
			"/memory [add|global|...] — 查看/编辑记忆文件\n" +
			"/allow <工具名> — 预授权工具\n" +
			"/lang [en|zh|...] — 查看/切换语言",
		LangTraditionalChinese: "**Agent 配置**\n" +
			"/model [switch <名稱>] — 查看/切換模型\n" +
			"/mode [名稱] — 查看/切換權限模式\n" +
			"/provider [list|add|...] — 管理 API Provider\n" +
			"/memory [add|global|...] — 查看/編輯記憶檔案\n" +
			"/allow <工具名> — 預授權工具\n" +
			"/lang [en|zh|...] — 查看/切換語言",
		LangJapanese: "**エージェント設定**\n" +
			"/model [switch <名前>] — モデルの表示/切り替え\n" +
			"/mode [名前] — 権限モードの表示/切り替え\n" +
			"/provider [list|add|...] — API プロバイダ管理\n" +
			"/memory [add|global|...] — メモリの表示/編集\n" +
			"/allow <ツール名> — ツールを事前許可\n" +
			"/lang [en|zh|...] — 言語の表示/切り替え",
		LangSpanish: "**Configuración del agente**\n" +
			"/model [switch <nombre>] — Ver/cambiar modelo\n" +
			"/mode [nombre] — Ver/cambiar modo de permisos\n" +
			"/provider [list|add|...] — Gestionar proveedores\n" +
			"/memory [add|global|...] — Ver/editar memoria\n" +
			"/allow <herramienta> — Pre-autorizar herramienta\n" +
			"/lang [en|zh|...] — Ver/cambiar idioma",
	},
	MsgHelpToolsSection: {
		LangEnglish: "**Tools & Automation**\n" +
			"/shell <command> — Run a shell command (! shortcut)\n" +
			"/show <ref> — View file / directory / snippet by reference\n" +
			"/dir [path|reset] — Show, switch, or reset work directory\n" +
			"/cron [add|list|exec|del|...] — Scheduled tasks\n" +
			"/timer [add|list|del|...] — One-shot timers\n" +
			"/commands [add|del] — Custom commands\n" +
			"/alias [add|del] — Command aliases\n" +
			"/skills — List agent skills\n" +
			"/compress — Compress context\n" +
			"/stop — Stop current execution",
		LangChinese: "**工具与自动化**\n" +
			"/shell <命令> — 执行 Shell 命令（!快捷方式）\n" +
			"/show <引用> — 按引用查看文件、目录或代码片段\n" +
			"/dir [路径|reset] — 查看、切换或重置工作目录\n" +
			"/cron [add|list|exec|del|...] — 定时任务\n" +
			"/timer [add|list|del|...] — 一次性定时器\n" +
			"/commands [add|del] — 自定义命令\n" +
			"/alias [add|del] — 命令别名\n" +
			"/skills — 列出 Agent Skills\n" +
			"/compress — 压缩上下文\n" +
			"/stop — 停止当前执行",
		LangTraditionalChinese: "**工具與自動化**\n" +
			"/shell <命令> — 執行 Shell 命令（!快捷方式）\n" +
			"/show <引用> — 按引用查看檔案、目錄或程式碼片段\n" +
			"/dir [路徑|reset] — 查看、切換或重置工作目錄\n" +
			"/cron [add|list|exec|del|...] — 定時任務\n" +
			"/timer [add|list|del|...] — 一次性定時器\n" +
			"/commands [add|del] — 自訂命令\n" +
			"/alias [add|del] — 命令別名\n" +
			"/skills — 列出 Agent Skills\n" +
			"/compress — 壓縮上下文\n" +
			"/stop — 停止當前執行",
		LangJapanese: "**ツール・自動化**\n" +
			"/shell <コマンド> — シェルコマンド実行（!ショートカット）\n" +
			"/show <参照> — ファイル/ディレクトリ/スニペットを参照で表示\n" +
			"/dir [パス|reset] — 作業ディレクトリの表示/切り替え/リセット\n" +
			"/cron [add|list|exec|del|...] — スケジュールタスク\n" +
			"/timer [add|list|del|...] — ワンショットタイマー\n" +
			"/commands [add|del] — カスタムコマンド\n" +
			"/alias [add|del] — コマンドエイリアス\n" +
			"/skills — エージェントスキル一覧\n" +
			"/compress — コンテキスト圧縮\n" +
			"/stop — 現在の実行を停止",
		LangSpanish: "**Herramientas y automatización**\n" +
			"/shell <comando> — Ejecutar comando shell (! atajo)\n" +
			"/show <ref> — Ver archivo/directorio/fragmento por referencia\n" +
			"/dir [ruta|reset] — Ver, cambiar o restablecer directorio de trabajo\n" +
			"/cron [add|list|exec|del|...] — Tareas programadas\n" +
			"/timer [add|list|del|...] — Temporizadores de uso único\n" +
			"/commands [add|del] — Comandos personalizados\n" +
			"/alias [add|del] — Alias de comandos\n" +
			"/skills — Listar skills del agente\n" +
			"/compress — Comprimir contexto\n" +
			"/stop — Detener ejecución actual",
	},
	MsgHelpSystemSection: {
		LangEnglish: "**System**\n" +
			"/config [get|set|reload] — Runtime configuration\n" +
			"/doctor — System diagnostics\n" +
			"/usage — Account/model quota usage\n" +
			"/whoami — Show your User ID\n" +
			"/upgrade — Check for updates\n" +
			"/restart — Restart service\n" +
			"/status — System status\n" +
			"/version — Show version",
		LangChinese: "**系统**\n" +
			"/config [get|set|reload] — 运行时配置\n" +
			"/doctor — 系统诊断\n" +
			"/usage — 账号/模型限额\n" +
			"/whoami — 查看你的 User ID\n" +
			"/upgrade — 检查更新\n" +
			"/restart — 重启服务\n" +
			"/status — 系统状态\n" +
			"/version — 查看版本",
		LangTraditionalChinese: "**系統**\n" +
			"/config [get|set|reload] — 執行階段配置\n" +
			"/doctor — 系統診斷\n" +
			"/usage — 帳號/模型限額\n" +
			"/whoami — 查看你的 User ID\n" +
			"/upgrade — 檢查更新\n" +
			"/restart — 重啟服務\n" +
			"/status — 系統狀態\n" +
			"/version — 查看版本",
		LangJapanese: "**システム**\n" +
			"/config [get|set|reload] — ランタイム設定\n" +
			"/doctor — システム診断\n" +
			"/usage — アカウント/モデル使用量\n" +
			"/whoami — User ID を表示\n" +
			"/upgrade — アップデート確認\n" +
			"/restart — サービス再起動\n" +
			"/status — システム状態\n" +
			"/version — バージョン表示",
		LangSpanish: "**Sistema**\n" +
			"/config [get|set|reload] — Configuración\n" +
			"/doctor — Diagnósticos del sistema\n" +
			"/usage — Uso de cuota de cuenta/modelo\n" +
			"/whoami — Mostrar tu User ID\n" +
			"/upgrade — Buscar actualizaciones\n" +
			"/restart — Reiniciar servicio\n" +
			"/status — Estado del sistema\n" +
			"/version — Mostrar versión",
	},
	MsgHelpTip: {
		LangEnglish:            "Tip: Commands support prefix matching, e.g. /pro l = /provider list",
		LangChinese:            "提示：命令支持前缀匹配，如 /pro l = /provider list",
		LangTraditionalChinese: "提示：命令支持前綴匹配，如 /pro l = /provider list",
		LangJapanese:           "ヒント：コマンドはプレフィックスマッチに対応、例: /pro l = /provider list",
		LangSpanish:            "Consejo: Los comandos admiten coincidencia por prefijo, ej. /pro l = /provider list",
	},
	MsgListTitle: {
		LangEnglish:            "**%s Sessions** (%d)\n\n",
		LangChinese:            "**%s 会话列表** (%d)\n\n",
		LangTraditionalChinese: "**%s 會話列表** (%d)\n\n",
		LangJapanese:           "**%s セッション** (%d)\n\n",
		LangSpanish:            "**Sesiones de %s** (%d)\n\n",
	},
	MsgListTitlePaged: {
		LangEnglish:            "**%s Sessions** (%d) · Page %d/%d\n\n",
		LangChinese:            "**%s 会话列表** (%d) · 第 %d/%d 页\n\n",
		LangTraditionalChinese: "**%s 會話列表** (%d) · 第 %d/%d 頁\n\n",
		LangJapanese:           "**%s セッション** (%d) · %d/%d ページ\n\n",
		LangSpanish:            "**Sesiones de %s** (%d) · Página %d/%d\n\n",
	},
	MsgListEmpty: {
		LangEnglish:            "No sessions found for this project.",
		LangChinese:            "未找到此项目的会话。",
		LangTraditionalChinese: "未找到此項目的會話。",
		LangJapanese:           "このプロジェクトのセッションが見つかりません。",
		LangSpanish:            "No se encontraron sesiones para este proyecto.",
	},
	MsgListMore: {
		LangEnglish:            "\n... and %d more\n",
		LangChinese:            "\n... 还有 %d 条\n",
		LangTraditionalChinese: "\n... 還有 %d 條\n",
		LangJapanese:           "\n... 他 %d 件\n",
		LangSpanish:            "\n... y %d más\n",
	},
	MsgListPageHint: {
		LangEnglish:            "\n\nPage %d/%d \n\n`/list <page>` for more\n",
		LangChinese:            "\n\n第 %d/%d 页 \n\n`/list <页码>` 翻页\n",
		LangTraditionalChinese: "\n\n第 %d/%d 頁 \n\n`/list <頁碼>` 翻頁\n",
		LangJapanese:           "\n\n%d/%d ページ \n\n`/list <ページ>` で移動\n",
		LangSpanish:            "\n\nPágina %d/%d \n\n`/list <página>` para más\n",
	},
	MsgListSwitchHint: {
		LangEnglish:            "\n`/switch <number>` to switch session",
		LangChinese:            "\n`/switch <序号>` 切换会话",
		LangTraditionalChinese: "\n`/switch <序號>` 切換會話",
		LangJapanese:           "\n`/switch <番号>` でセッション切替",
		LangSpanish:            "\n`/switch <número>` para cambiar sesión",
	},
	MsgListError: {
		LangEnglish:            "❌ Failed to list sessions: %v",
		LangChinese:            "❌ 获取会话列表失败: %v",
		LangTraditionalChinese: "❌ 取得會話列表失敗: %v",
		LangJapanese:           "❌ セッション一覧の取得に失敗しました: %v",
		LangSpanish:            "❌ Error al listar sesiones: %v",
	},
	MsgHistoryEmpty: {
		LangEnglish:            "No history in current session.",
		LangChinese:            "当前会话暂无历史消息。",
		LangTraditionalChinese: "當前會話暫無歷史訊息。",
		LangJapanese:           "現在のセッションに履歴がありません。",
		LangSpanish:            "No hay historial en la sesión actual.",
	},
	MsgNameUsage: {
		LangEnglish:            "Usage:\n`/name <text>` — name the current session\n`/name <number> <text>` — name a session by list number",
		LangChinese:            "用法：\n`/name <名称>` — 命名当前会话\n`/name <序号> <名称>` — 按列表序号命名会话",
		LangTraditionalChinese: "用法：\n`/name <名稱>` — 命名當前會話\n`/name <序號> <名稱>` — 按列表序號命名會話",
		LangJapanese:           "使い方：\n`/name <名前>` — 現在のセッションに名前を付ける\n`/name <番号> <名前>` — リスト番号でセッションに名前を付ける",
		LangSpanish:            "Uso:\n`/name <texto>` — nombrar la sesión actual\n`/name <número> <texto>` — nombrar una sesión por número de lista",
	},
	MsgNameSet: {
		LangEnglish:            "✅ Session named: **%s** (%s)",
		LangChinese:            "✅ 会话已命名：**%s** (%s)",
		LangTraditionalChinese: "✅ 會話已命名：**%s** (%s)",
		LangJapanese:           "✅ セッション名設定：**%s** (%s)",
		LangSpanish:            "✅ Sesión nombrada: **%s** (%s)",
	},
	MsgNameNoSession: {
		LangEnglish:            "❌ No active session. Send a message first or switch to a session.",
		LangChinese:            "❌ 没有活跃会话，请先发送消息或切换到一个会话。",
		LangTraditionalChinese: "❌ 沒有活躍會話，請先傳送訊息或切換到一個會話。",
		LangJapanese:           "❌ アクティブなセッションがありません。メッセージを送信するかセッションに切り替えてください。",
		LangSpanish:            "❌ No hay sesión activa. Envía un mensaje primero o cambia a una sesión.",
	},
	MsgProviderNotSupported: {
		LangEnglish:            "This agent does not support provider switching.",
		LangChinese:            "当前 Agent 不支持 Provider 切换。",
		LangTraditionalChinese: "當前 Agent 不支援 Provider 切換。",
		LangJapanese:           "このエージェントはプロバイダの切り替えをサポートしていません。",
		LangSpanish:            "Este agente no soporta el cambio de proveedor.",
	},
	MsgProviderNone: {
		LangEnglish:            "No provider configured. Using agent's default environment.\n\nAdd providers in `config.toml` or via `lark-agent-bot provider add`.",
		LangChinese:            "未配置 Provider，使用 Agent 默认环境。\n\n可在 `config.toml` 中添加或使用 `lark-agent-bot provider add` 命令。",
		LangTraditionalChinese: "未配置 Provider，使用 Agent 預設環境。\n\n可在 `config.toml` 中新增或使用 `lark-agent-bot provider add` 命令。",
		LangJapanese:           "プロバイダが設定されていません。エージェントのデフォルト環境を使用します。\n\n`config.toml` または `lark-agent-bot provider add` でプロバイダを追加してください。",
		LangSpanish:            "No hay proveedor configurado. Usando el entorno predeterminado del agente.\n\nAgregue proveedores en `config.toml` o mediante `lark-agent-bot provider add`.",
	},
	MsgProviderCurrent: {
		LangEnglish:            "📡 Active provider: **%s**\n\nUse `/provider list` to see all, `/provider switch <name>` to switch.",
		LangChinese:            "📡 当前 Provider: **%s**\n\n使用 `/provider list` 查看全部，`/provider switch <名称>` 切换。",
		LangTraditionalChinese: "📡 當前 Provider: **%s**\n\n使用 `/provider list` 查看全部，`/provider switch <名稱>` 切換。",
		LangJapanese:           "📡 現在のプロバイダ: **%s**\n\n`/provider list` で一覧、`/provider switch <名前>` で切り替え。",
		LangSpanish:            "📡 Proveedor activo: **%s**\n\nUse `/provider list` para ver todos, `/provider switch <nombre>` para cambiar.",
	},
	MsgProviderListTitle: {
		LangEnglish:            "📡 Providers\n\n",
		LangChinese:            "📡 Provider 列表\n\n",
		LangTraditionalChinese: "📡 Provider 列表\n\n",
		LangJapanese:           "📡 プロバイダ一覧\n\n",
		LangSpanish:            "📡 Proveedores\n\n",
	},
	MsgProviderListEmpty: {
		LangEnglish:            "No providers configured.\n\nAdd providers in `config.toml` or via `lark-agent-bot provider add`.",
		LangChinese:            "未配置 Provider。\n\n可在 `config.toml` 中添加或使用 `lark-agent-bot provider add` 命令。",
		LangTraditionalChinese: "未配置 Provider。\n\n可在 `config.toml` 中新增或使用 `lark-agent-bot provider add` 命令。",
		LangJapanese:           "プロバイダが設定されていません。\n\n`config.toml` または `lark-agent-bot provider add` で追加してください。",
		LangSpanish:            "No hay proveedores configurados.\n\nAgregue proveedores en `config.toml` o mediante `lark-agent-bot provider add`.",
	},
	MsgProviderSwitchHint: {
		LangEnglish:            "`/provider switch <name>` to switch | `/provider clear` to reset",
		LangChinese:            "`/provider switch <名称>` 切换 | `/provider clear` 清除",
		LangTraditionalChinese: "`/provider switch <名稱>` 切換 | `/provider clear` 清除",
		LangJapanese:           "`/provider switch <名前>` で切り替え | `/provider clear` でリセット",
		LangSpanish:            "`/provider switch <nombre>` para cambiar | `/provider clear` para restablecer",
	},
	MsgProviderNotFound: {
		LangEnglish:            "❌ Provider %q not found. Use `/provider list` to see available providers.",
		LangChinese:            "❌ 未找到 Provider %q。使用 `/provider list` 查看可用列表。",
		LangTraditionalChinese: "❌ 未找到 Provider %q。使用 `/provider list` 查看可用列表。",
		LangJapanese:           "❌ プロバイダ %q が見つかりません。`/provider list` で一覧を確認してください。",
		LangSpanish:            "❌ Proveedor %q no encontrado. Use `/provider list` para ver los disponibles.",
	},
	MsgProviderSwitched: {
		LangEnglish:            "✅ Provider switched to **%s**. New sessions will use this provider.",
		LangChinese:            "✅ Provider 已切换为 **%s**，新会话将使用此 Provider。",
		LangTraditionalChinese: "✅ Provider 已切換為 **%s**，新會話將使用此 Provider。",
		LangJapanese:           "✅ プロバイダを **%s** に切り替えました。新しいセッションで使用されます。",
		LangSpanish:            "✅ Proveedor cambiado a **%s**. Las nuevas sesiones usarán este proveedor.",
	},
	MsgProviderCleared: {
		LangEnglish:            "✅ Provider cleared. New sessions will use the default provider.",
		LangChinese:            "✅ Provider 已清除，新会话将使用默认 Provider。",
		LangTraditionalChinese: "✅ Provider 已清除，新會話將使用預設 Provider。",
		LangJapanese:           "✅ プロバイダをクリアしました。新しいセッションではデフォルトのプロバイダが使用されます。",
		LangSpanish:            "✅ Proveedor eliminado. Las nuevas sesiones usarán el proveedor predeterminado.",
	},
	MsgProviderAdded: {
		LangEnglish:            "✅ Provider **%s** added.\n\nUse `/provider switch %s` to activate.",
		LangChinese:            "✅ Provider **%s** 已添加。\n\n使用 `/provider switch %s` 激活。",
		LangTraditionalChinese: "✅ Provider **%s** 已新增。\n\n使用 `/provider switch %s` 啟用。",
		LangJapanese:           "✅ プロバイダ **%s** を追加しました。\n\n`/provider switch %s` で有効化してください。",
		LangSpanish:            "✅ Proveedor **%s** agregado.\n\nUse `/provider switch %s` para activarlo.",
	},
	MsgProviderAddUsage: {
		LangEnglish: "Usage:\n\n" +
			"`/provider add <name> <api_key> [base_url] [model]`\n\n" +
			"Or JSON:\n" +
			"`/provider add {\"name\":\"relay\",\"api_key\":\"sk-xxx\",\"base_url\":\"https://...\",\"model\":\"...\"}`",
		LangChinese: "用法:\n\n" +
			"`/provider add <名称> <api_key> [base_url] [model]`\n\n" +
			"或 JSON:\n" +
			"`/provider add {\"name\":\"relay\",\"api_key\":\"sk-xxx\",\"base_url\":\"https://...\",\"model\":\"...\"}`",
		LangTraditionalChinese: "用法:\n\n" +
			"`/provider add <名稱> <api_key> [base_url] [model]`\n\n" +
			"或 JSON:\n" +
			"`/provider add {\"name\":\"relay\",\"api_key\":\"sk-xxx\",\"base_url\":\"https://...\",\"model\":\"...\"}`",
		LangJapanese: "使い方:\n\n" +
			"`/provider add <名前> <api_key> [base_url] [model]`\n\n" +
			"または JSON:\n" +
			"`/provider add {\"name\":\"relay\",\"api_key\":\"sk-xxx\",\"base_url\":\"https://...\",\"model\":\"...\"}`",
		LangSpanish: "Uso:\n\n" +
			"`/provider add <nombre> <api_key> [base_url] [model]`\n\n" +
			"O JSON:\n" +
			"`/provider add {\"name\":\"relay\",\"api_key\":\"sk-xxx\",\"base_url\":\"https://...\",\"model\":\"...\"}`",
	},
	MsgProviderAddFailed: {
		LangEnglish:            "❌ Failed to add provider: %v",
		LangChinese:            "❌ 添加 Provider 失败: %v",
		LangTraditionalChinese: "❌ 新增 Provider 失敗: %v",
		LangJapanese:           "❌ プロバイダの追加に失敗しました: %v",
		LangSpanish:            "❌ Error al agregar proveedor: %v",
	},
	MsgProviderRemoved: {
		LangEnglish:            "✅ Provider **%s** removed.",
		LangChinese:            "✅ Provider **%s** 已移除。",
		LangTraditionalChinese: "✅ Provider **%s** 已移除。",
		LangJapanese:           "✅ プロバイダ **%s** を削除しました。",
		LangSpanish:            "✅ Proveedor **%s** eliminado.",
	},
	MsgProviderRemoveFailed: {
		LangEnglish:            "❌ Failed to remove provider: %v",
		LangChinese:            "❌ 移除 Provider 失败: %v",
		LangTraditionalChinese: "❌ 移除 Provider 失敗: %v",
		LangJapanese:           "❌ プロバイダの削除に失敗しました: %v",
		LangSpanish:            "❌ Error al eliminar proveedor: %v",
	},
	MsgCardTitleProviderAdd: {
		LangEnglish: "Add Provider", LangChinese: "添加服务商", LangTraditionalChinese: "新增服務商",
		LangJapanese: "プロバイダーを追加", LangSpanish: "Añadir proveedor",
	},
	MsgProviderAddPickHint: {
		LangEnglish:            "Pick a provider below, or choose **Other** to enter manually.\nAfter selecting, send your API key to complete.",
		LangChinese:            "选择一个服务商，或选择 **自定义** 手动填写。\n选择后，请发送你的 API Key 来完成添加。",
		LangTraditionalChinese: "選擇一個服務商，或選擇 **自訂** 手動填寫。\n選擇後，請傳送你的 API Key 來完成新增。",
		LangJapanese:           "プロバイダーを選択するか、**その他** を選んで手動入力してください。\n選択後、API キーを送信して完了します。",
		LangSpanish:            "Elige un proveedor o selecciona **Otro** para ingresar manualmente.\nDespués de seleccionar, envía tu API Key para completar.",
	},
	MsgProviderAddOther: {
		LangEnglish: "Other (manual)", LangChinese: "自定义 (手动)", LangTraditionalChinese: "自訂 (手動)",
		LangJapanese: "その他 (手動)", LangSpanish: "Otro (manual)",
	},
	MsgProviderAddApiKeyPrompt: {
		LangEnglish:            "✅ Selected **%s**.\n\nPlease send your **API Key** for this provider.\nFormat: just the key, e.g. `sk-xxxxxxxx`",
		LangChinese:            "✅ 已选择 **%s**。\n\n请发送你的 **API Key**。\n格式：直接发送密钥即可，如 `sk-xxxxxxxx`",
		LangTraditionalChinese: "✅ 已選擇 **%s**。\n\n請傳送你的 **API Key**。\n格式：直接傳送金鑰即可，如 `sk-xxxxxxxx`",
		LangJapanese:           "✅ **%s** を選択しました。\n\n**API キー** を送信してください。\n形式: キーをそのまま送信（例: `sk-xxxxxxxx`）",
		LangSpanish:            "✅ Seleccionado **%s**.\n\nPor favor envía tu **API Key** para este proveedor.\nFormato: solo la clave, por ejemplo `sk-xxxxxxxx`",
	},
	MsgProviderAddInviteHint: {
		LangEnglish:            "🔑 Don't have a key? Register here: %s",
		LangChinese:            "🔑 还没有 Key？点击注册获取：%s",
		LangTraditionalChinese: "🔑 還沒有 Key？點擊註冊取得：%s",
		LangJapanese:           "🔑 キーをお持ちでない場合はこちらから登録: %s",
		LangSpanish:            "🔑 ¿No tienes una clave? Regístrate aquí: %s",
	},
	MsgProviderLinkGlobal: {
		LangEnglish: "Link existing provider", LangChinese: "关联已有服务商", LangTraditionalChinese: "關聯已有服務商",
		LangJapanese: "既存プロバイダーをリンク", LangSpanish: "Vincular proveedor existente",
	},
	MsgProviderLinked: {
		LangEnglish:            "✅ Provider **%s** linked to this project.",
		LangChinese:            "✅ 已关联服务商 **%s** 到当前项目。",
		LangTraditionalChinese: "✅ 已關聯服務商 **%s** 到目前專案。",
		LangJapanese:           "✅ プロバイダー **%s** をこのプロジェクトにリンクしました。",
		LangSpanish:            "✅ Proveedor **%s** vinculado a este proyecto.",
	},
	MsgVoiceNotEnabled: {
		LangEnglish:            "🎙 Voice messages are not enabled. Please configure `[speech]` in config.toml.",
		LangChinese:            "🎙 语音消息未启用，请在 config.toml 中配置 `[speech]` 部分。",
		LangTraditionalChinese: "🎙 語音訊息未啟用，請在 config.toml 中配置 `[speech]` 部分。",
		LangJapanese:           "🎙 音声メッセージは有効になっていません。config.toml で `[speech]` を設定してください。",
		LangSpanish:            "🎙 Los mensajes de voz no están habilitados. Configure `[speech]` en config.toml.",
	},
	MsgVoiceUsingPlatformRecognition: {
		LangEnglish:            "⚠️ Voice transcription not configured, using %s built-in recognition",
		LangChinese:            "⚠️ 未配置语音转录，使用 %s 内置语音识别",
		LangTraditionalChinese: "⚠️ 未配置語音轉錄，使用 %s 內置語音識別",
		LangJapanese:           "⚠️ 音声転写が設定されていないため、%s の組み込み認識を使用",
		LangSpanish:            "⚠️ Transcripción de voz no configurada, usando reconocimiento integrado de %s",
	},
	MsgVoiceNoFFmpeg: {
		LangEnglish:            "🎙 Voice message requires `ffmpeg` for format conversion. Please install ffmpeg.",
		LangChinese:            "🎙 语音消息需要 `ffmpeg` 进行格式转换，请安装 ffmpeg。",
		LangTraditionalChinese: "🎙 語音訊息需要 `ffmpeg` 進行格式轉換，請安裝 ffmpeg。",
		LangJapanese:           "🎙 音声メッセージのフォーマット変換に `ffmpeg` が必要です。ffmpeg をインストールしてください。",
		LangSpanish:            "🎙 Los mensajes de voz requieren `ffmpeg` para la conversión de formato. Instale ffmpeg.",
	},
	MsgVoiceTranscribing: {
		LangEnglish:            "🎙 Transcribing voice message...",
		LangChinese:            "🎙 正在转录语音消息...",
		LangTraditionalChinese: "🎙 正在轉錄語音訊息...",
		LangJapanese:           "🎙 音声メッセージを文字起こし中...",
		LangSpanish:            "🎙 Transcribiendo mensaje de voz...",
	},
	MsgVoiceTranscribed: {
		LangEnglish:            "🎙 [Voice] %s",
		LangChinese:            "🎙 [语音] %s",
		LangTraditionalChinese: "🎙 [語音] %s",
		LangJapanese:           "🎙 [音声] %s",
		LangSpanish:            "🎙 [Voz] %s",
	},
	MsgVoiceTranscribeFailed: {
		LangEnglish:            "🎙 Voice transcription failed: %v",
		LangChinese:            "🎙 语音转文字失败: %v",
		LangTraditionalChinese: "🎙 語音轉文字失敗: %v",
		LangJapanese:           "🎙 音声の文字起こしに失敗しました: %v",
		LangSpanish:            "🎙 Error en la transcripción de voz: %v",
	},
	MsgVoiceEmpty: {
		LangEnglish:            "🎙 Voice message was empty or could not be recognized.",
		LangChinese:            "🎙 语音消息为空或无法识别。",
		LangTraditionalChinese: "🎙 語音訊息為空或無法識別。",
		LangJapanese:           "🎙 音声メッセージが空か、認識できませんでした。",
		LangSpanish:            "🎙 El mensaje de voz estaba vacío o no se pudo reconocer.",
	},
	MsgTTSNotEnabled: {
		LangEnglish:            "TTS is not enabled. Please configure `[tts]` in config.toml.",
		LangChinese:            "TTS 未启用，请在 config.toml 中配置 `[tts]` 部分。",
		LangTraditionalChinese: "TTS 未啟用，請在 config.toml 中配置 `[tts]` 部分。",
		LangJapanese:           "TTS は有効になっていません。config.toml で `[tts]` を設定してください。",
		LangSpanish:            "TTS no está habilitado. Configure `[tts]` en config.toml.",
	},
	MsgTTSStatus: {
		LangEnglish:            "TTS status: enabled=true, mode=%s, provider=%s",
		LangChinese:            "TTS 状态：enabled=true，mode=%s，provider=%s",
		LangTraditionalChinese: "TTS 狀態：enabled=true，mode=%s，provider=%s",
		LangJapanese:           "TTS 状態: enabled=true, mode=%s, provider=%s",
		LangSpanish:            "Estado TTS: enabled=true, mode=%s, provider=%s",
	},
	MsgTTSSwitched: {
		LangEnglish:            "TTS mode switched to: %s",
		LangChinese:            "TTS 已切换为 %s 模式",
		LangTraditionalChinese: "TTS 已切換為 %s 模式",
		LangJapanese:           "TTS モードを %s に切り替えました",
		LangSpanish:            "Modo TTS cambiado a: %s",
	},
	MsgTTSUsage: {
		LangEnglish:            "Usage: /tts [always|voice_only]",
		LangChinese:            "用法：/tts [always|voice_only]",
		LangTraditionalChinese: "用法：/tts [always|voice_only]",
		LangJapanese:           "使い方: /tts [always|voice_only]",
		LangSpanish:            "Uso: /tts [always|voice_only]",
	},
	MsgHeartbeatNotAvailable: {
		LangEnglish:            "Heartbeat is not configured for this project.",
		LangChinese:            "当前项目未配置心跳。",
		LangTraditionalChinese: "當前項目未配置心跳。",
		LangJapanese:           "このプロジェクトにはハートビートが設定されていません。",
		LangSpanish:            "El heartbeat no está configurado para este proyecto.",
	},
	MsgHeartbeatStatus: {
		LangEnglish: "💓 Heartbeat Status\n\n" +
			"State: %s\n" +
			"Interval: %d min\n" +
			"Only when idle: %s\n" +
			"Silent: %s\n" +
			"Runs: %d\n" +
			"Errors: %d\n" +
			"Skipped (busy): %d\n" +
			"%s",
		LangChinese: "💓 心跳状态\n\n" +
			"状态: %s\n" +
			"间隔: %d 分钟\n" +
			"仅空闲时: %s\n" +
			"静默: %s\n" +
			"执行次数: %d\n" +
			"失败次数: %d\n" +
			"跳过 (忙碌): %d\n" +
			"%s",
		LangTraditionalChinese: "💓 心跳狀態\n\n" +
			"狀態: %s\n" +
			"間隔: %d 分鐘\n" +
			"僅空閒時: %s\n" +
			"靜默: %s\n" +
			"執行次數: %d\n" +
			"失敗次數: %d\n" +
			"跳過 (忙碌): %d\n" +
			"%s",
		LangJapanese: "💓 ハートビート状態\n\n" +
			"状態: %s\n" +
			"間隔: %d 分\n" +
			"アイドル時のみ: %s\n" +
			"サイレント: %s\n" +
			"実行回数: %d\n" +
			"エラー: %d\n" +
			"スキップ (ビジー): %d\n" +
			"%s",
		LangSpanish: "💓 Estado del Heartbeat\n\n" +
			"Estado: %s\n" +
			"Intervalo: %d min\n" +
			"Solo cuando inactivo: %s\n" +
			"Silencioso: %s\n" +
			"Ejecuciones: %d\n" +
			"Errores: %d\n" +
			"Omitidos (ocupado): %d\n" +
			"%s",
	},
	MsgHeartbeatPaused: {
		LangEnglish:            "💓 Heartbeat paused.",
		LangChinese:            "💓 心跳已暂停。",
		LangTraditionalChinese: "💓 心跳已暫停。",
		LangJapanese:           "💓 ハートビートを一時停止しました。",
		LangSpanish:            "💓 Heartbeat pausado.",
	},
	MsgHeartbeatResumed: {
		LangEnglish:            "💓 Heartbeat resumed.",
		LangChinese:            "💓 心跳已恢复。",
		LangTraditionalChinese: "💓 心跳已恢復。",
		LangJapanese:           "💓 ハートビートを再開しました。",
		LangSpanish:            "💓 Heartbeat reanudado.",
	},
	MsgHeartbeatInterval: {
		LangEnglish:            "💓 Heartbeat interval changed to %d minutes.",
		LangChinese:            "💓 心跳间隔已调整为 %d 分钟。",
		LangTraditionalChinese: "💓 心跳間隔已調整為 %d 分鐘。",
		LangJapanese:           "💓 ハートビート間隔を %d 分に変更しました。",
		LangSpanish:            "💓 Intervalo del heartbeat cambiado a %d minutos.",
	},
	MsgHeartbeatTriggered: {
		LangEnglish:            "💓 Heartbeat triggered.",
		LangChinese:            "💓 心跳已触发。",
		LangTraditionalChinese: "💓 心跳已觸發。",
		LangJapanese:           "💓 ハートビートをトリガーしました。",
		LangSpanish:            "💓 Heartbeat activado.",
	},
	MsgHeartbeatUsage: {
		LangEnglish:            "Usage: /heartbeat [status|pause|resume|run|interval <mins>]",
		LangChinese:            "用法: /heartbeat [status|pause|resume|run|interval <分钟>]",
		LangTraditionalChinese: "用法: /heartbeat [status|pause|resume|run|interval <分鐘>]",
		LangJapanese:           "使い方: /heartbeat [status|pause|resume|run|interval <分>]",
		LangSpanish:            "Uso: /heartbeat [status|pause|resume|run|interval <minutos>]",
	},
	MsgHeartbeatInvalidMins: {
		LangEnglish:            "Invalid interval. Please provide a positive number of minutes.",
		LangChinese:            "无效的间隔。请输入正整数的分钟数。",
		LangTraditionalChinese: "無效的間隔。請輸入正整數的分鐘數。",
		LangJapanese:           "無効な間隔です。正の整数を分で指定してください。",
		LangSpanish:            "Intervalo inválido. Proporcione un número positivo de minutos.",
	},
	MsgCronNotAvailable: {
		LangEnglish:            "Cron scheduler is not available.",
		LangChinese:            "定时任务调度器未启用。",
		LangTraditionalChinese: "定時任務調度器未啟用。",
		LangJapanese:           "スケジューラは利用できません。",
		LangSpanish:            "El programador de tareas no está disponible.",
	},
	MsgCronUsage: {
		LangEnglish:            "Usage:\n/cron add <min> <hour> <day> <month> <weekday> <prompt>\n/cron list\n/cron exec <id>\n/cron del <id>\n/cron enable <id> · /cron disable <id>\n/cron mute <id> · /cron unmute <id>\n/cron setup — write lark-agent-bot instructions to agent memory file",
		LangChinese:            "用法：\n/cron add <分> <时> <日> <月> <周> <任务描述>\n/cron list\n/cron exec <id> 立即执行\n/cron del <id>\n/cron enable <id> · /cron disable <id>\n/cron mute <id> · /cron unmute <id> 静音/取消静音\n/cron setup — 将 lark-agent-bot 指令写入 agent 记忆文件",
		LangTraditionalChinese: "用法：\n/cron add <分> <時> <日> <月> <週> <任務描述>\n/cron list\n/cron exec <id> 立即執行\n/cron del <id>\n/cron enable <id> · /cron disable <id>\n/cron mute <id> · /cron unmute <id> 靜音/取消靜音\n/cron setup — 將 lark-agent-bot 指令寫入 agent 記憶檔案",
		LangJapanese:           "使い方:\n/cron add <分> <時> <日> <月> <曜日> <タスク内容>\n/cron list\n/cron exec <id> 今すぐ実行\n/cron del <id>\n/cron enable <id> · /cron disable <id>\n/cron mute <id> · /cron unmute <id> ミュート/解除\n/cron setup — lark-agent-bot の指示をエージェントのメモリファイルに書き込む",
		LangSpanish:            "Uso:\n/cron add <min> <hora> <día> <mes> <día_semana> <tarea>\n/cron list\n/cron exec <id>\n/cron del <id>\n/cron enable <id> · /cron disable <id>\n/cron mute <id> · /cron unmute <id>\n/cron setup — escribir las instrucciones de lark-agent-bot en el archivo de memoria del agente",
	},
	MsgCronAddUsage: {
		LangEnglish:            "Usage: /cron add <min> <hour> <day> <month> <weekday> <prompt>\nExample: /cron add 0 6 * * * Collect GitHub trending data and send me a summary",
		LangChinese:            "用法：/cron add <分> <时> <日> <月> <周> <任务描述>\n示例：/cron add 0 6 * * * 收集 GitHub Trending 数据整理成简报发给我",
		LangTraditionalChinese: "用法：/cron add <分> <時> <日> <月> <週> <任務描述>\n範例：/cron add 0 6 * * * 收集 GitHub Trending 資料整理成簡報發給我",
		LangJapanese:           "使い方: /cron add <分> <時> <日> <月> <曜日> <タスク内容>\n例: /cron add 0 6 * * * GitHub Trending を収集してまとめを送って",
		LangSpanish:            "Uso: /cron add <min> <hora> <día> <mes> <día_semana> <tarea>\nEjemplo: /cron add 0 6 * * * Recopilar datos de GitHub Trending y enviarme un resumen",
	},
	MsgCronAdded: {
		LangEnglish:            "✅ Cron job created\nID: `%s`\nSchedule: `%s`\nPrompt: %s",
		LangChinese:            "✅ 定时任务已创建\nID: `%s`\n调度: `%s`\n内容: %s",
		LangTraditionalChinese: "✅ 定時任務已建立\nID: `%s`\n調度: `%s`\n內容: %s",
		LangJapanese:           "✅ スケジュールタスクを作成しました\nID: `%s`\nスケジュール: `%s`\n内容: %s",
		LangSpanish:            "✅ Tarea programada creada\nID: `%s`\nProgramación: `%s`\nContenido: %s",
	},
	MsgCronAddedExec: {
		LangEnglish:            "✅ Shell cron job created\nID: `%s`\nSchedule: `%s`\nCommand: `%s`",
		LangChinese:            "✅ Shell 定时任务已创建\nID: `%s`\n调度: `%s`\n命令: `%s`",
		LangTraditionalChinese: "✅ Shell 定時任務已建立\nID: `%s`\n調度: `%s`\n命令: `%s`",
		LangJapanese:           "✅ Shell スケジュールタスクを作成しました\nID: `%s`\nスケジュール: `%s`\nコマンド: `%s`",
		LangSpanish:            "✅ Tarea shell programada creada\nID: `%s`\nProgramación: `%s`\nComando: `%s`",
	},
	MsgCronAddExecUsage: {
		LangEnglish:            "Usage: /cron addexec <min> <hour> <day> <month> <weekday> <shell command>\nExample: /cron addexec 0 6 * * * df -h",
		LangChinese:            "用法：/cron addexec <分> <时> <日> <月> <周> <shell 命令>\n示例：/cron addexec 0 6 * * * df -h",
		LangTraditionalChinese: "用法：/cron addexec <分> <時> <日> <月> <週> <shell 命令>\n範例：/cron addexec 0 6 * * * df -h",
		LangJapanese:           "使い方: /cron addexec <分> <時> <日> <月> <曜日> <シェルコマンド>\n例: /cron addexec 0 6 * * * df -h",
		LangSpanish:            "Uso: /cron addexec <min> <hora> <día> <mes> <día_semana> <comando shell>\nEjemplo: /cron addexec 0 6 * * * df -h",
	},
	MsgCronEmpty: {
		LangEnglish:            "No recurring tasks.\n(For one-shot reminders/delays, use /timer)",
		LangChinese:            "暂无周期任务。\n（一次性提醒/延迟任务请用 /timer 查看）",
		LangTraditionalChinese: "暫無週期任務。\n（一次性提醒/延遲任務請用 /timer 查看）",
		LangJapanese:           "繰り返しタスクはありません。\n（ワンショットのリマインダーは /timer をご利用ください）",
		LangSpanish:            "No hay tareas recurrentes.\n(Para recordatorios únicos use /timer)",
	},
	MsgCronListTitle: {
		LangEnglish:            "⏰ Scheduled Tasks (%d)",
		LangChinese:            "⏰ 定时任务 (%d)",
		LangTraditionalChinese: "⏰ 定時任務 (%d)",
		LangJapanese:           "⏰ スケジュールタスク (%d)",
		LangSpanish:            "⏰ Tareas programadas (%d)",
	},
	MsgCronListFooter: {
		LangEnglish:            "`/cron exec <id>` trigger now · `/cron del <id>` remove · `/cron enable/disable <id>` toggle · `/cron mute/unmute <id>` mute",
		LangChinese:            "`/cron exec <id>` 立即触发 · `/cron del <id>` 删除 · `/cron enable/disable <id>` 启停 · `/cron mute/unmute <id>` 静音",
		LangTraditionalChinese: "`/cron exec <id>` 立即觸發 · `/cron del <id>` 刪除 · `/cron enable/disable <id>` 啟停 · `/cron mute/unmute <id>` 靜音",
		LangJapanese:           "`/cron exec <id>` 今すぐ実行 · `/cron del <id>` 削除 · `/cron enable/disable <id>` 切替 · `/cron mute/unmute <id>` ミュート",
		LangSpanish:            "`/cron exec <id>` ejecutar ahora · `/cron del <id>` eliminar · `/cron enable/disable <id>` activar/desactivar · `/cron mute/unmute <id>` silenciar",
	},
	MsgCronExecUsage: {
		LangEnglish:            "Usage: /cron exec <id>",
		LangChinese:            "用法：/cron exec <id>",
		LangTraditionalChinese: "用法：/cron exec <id>",
		LangJapanese:           "使い方: /cron exec <id>",
		LangSpanish:            "Uso: /cron exec <id>",
	},
	MsgCronTriggered: {
		LangEnglish:            "▶️ Cron job `%s` triggered.",
		LangChinese:            "▶️ 定时任务 `%s` 已触发。",
		LangTraditionalChinese: "▶️ 定時任務 `%s` 已觸發。",
		LangJapanese:           "▶️ スケジュールタスク `%s` を実行しました。",
		LangSpanish:            "▶️ Tarea programada `%s` ejecutada.",
	},
	MsgCronProjectUnavailable: {
		LangEnglish:            "❌ This cron job cannot be triggered because its project is no longer available.",
		LangChinese:            "❌ 该定时任务关联的项目已不可用，无法触发。",
		LangTraditionalChinese: "❌ 該定時任務關聯的專案已不可用，無法觸發。",
		LangJapanese:           "❌ このスケジュールタスクは、関連するプロジェクトが利用できないため実行できません。",
		LangSpanish:            "❌ Esta tarea programada no puede ejecutarse porque su proyecto ya no está disponible.",
	},
	MsgCronDelUsage: {
		LangEnglish:            "Usage: /cron del <id>",
		LangChinese:            "用法：/cron del <id>",
		LangTraditionalChinese: "用法：/cron del <id>",
		LangJapanese:           "使い方: /cron del <id>",
		LangSpanish:            "Uso: /cron del <id>",
	},
	MsgCronDeleted: {
		LangEnglish:            "✅ Cron job `%s` deleted.",
		LangChinese:            "✅ 定时任务 `%s` 已删除。",
		LangTraditionalChinese: "✅ 定時任務 `%s` 已刪除。",
		LangJapanese:           "✅ スケジュールタスク `%s` を削除しました。",
		LangSpanish:            "✅ Tarea programada `%s` eliminada.",
	},
	MsgCronNotFound: {
		LangEnglish:            "❌ Cron job `%s` not found.",
		LangChinese:            "❌ 定时任务 `%s` 未找到。",
		LangTraditionalChinese: "❌ 定時任務 `%s` 未找到。",
		LangJapanese:           "❌ スケジュールタスク `%s` が見つかりません。",
		LangSpanish:            "❌ Tarea programada `%s` no encontrada.",
	},
	MsgCronEnabled: {
		LangEnglish:            "✅ Cron job `%s` enabled.",
		LangChinese:            "✅ 定时任务 `%s` 已启用。",
		LangTraditionalChinese: "✅ 定時任務 `%s` 已啟用。",
		LangJapanese:           "✅ スケジュールタスク `%s` を有効にしました。",
		LangSpanish:            "✅ Tarea programada `%s` habilitada.",
	},
	MsgCronDisabled: {
		LangEnglish:            "⏸ Cron job `%s` disabled.",
		LangChinese:            "⏸ 定时任务 `%s` 已暂停。",
		LangTraditionalChinese: "⏸ 定時任務 `%s` 已暫停。",
		LangJapanese:           "⏸ スケジュールタスク `%s` を無効にしました。",
		LangSpanish:            "⏸ Tarea programada `%s` deshabilitada.",
	},
	MsgCronMuted: {
		LangEnglish:            "🔇 Cron job `%s` muted (all messages suppressed).",
		LangChinese:            "🔇 定时任务 `%s` 已静音（所有消息均不发送）。",
		LangTraditionalChinese: "🔇 定時任務 `%s` 已靜音（所有訊息均不發送）。",
		LangJapanese:           "🔇 スケジュールタスク `%s` をミュートしました（全メッセージ抑制）。",
		LangSpanish:            "🔇 Tarea programada `%s` silenciada (todos los mensajes suprimidos).",
	},
	MsgCronUnmuted: {
		LangEnglish:            "🔔 Cron job `%s` unmuted.",
		LangChinese:            "🔔 定时任务 `%s` 已取消静音。",
		LangTraditionalChinese: "🔔 定時任務 `%s` 已取消靜音。",
		LangJapanese:           "🔔 スケジュールタスク `%s` のミュートを解除しました。",
		LangSpanish:            "🔔 Tarea programada `%s` reactivada.",
	},
	MsgCronCardHint: {
		LangEnglish:            "💡 `/cron add` · `/cron exec <id>` · `/cron del <id>` · `/cron enable/disable <id>` · `/cron mute/unmute <id>`",
		LangChinese:            "💡 `/cron add` 添加 · `/cron exec <id>` 触发 · `/cron del <id>` 删除 · `/cron enable/disable <id>` 启停 · `/cron mute/unmute <id>` 静音",
		LangTraditionalChinese: "💡 `/cron add` 新增 · `/cron exec <id>` 觸發 · `/cron del <id>` 刪除 · `/cron enable/disable <id>` 啟停 · `/cron mute/unmute <id>` 靜音",
		LangJapanese:           "💡 `/cron add` 追加 · `/cron exec <id>` 実行 · `/cron del <id>` 削除 · `/cron enable/disable <id>` 切替 · `/cron mute/unmute <id>` ミュート",
		LangSpanish:            "💡 `/cron add` · `/cron exec <id>` · `/cron del <id>` · `/cron enable/disable <id>` · `/cron mute/unmute <id>`",
	},
	MsgCronBtnEnable: {
		LangEnglish:            "Enable",
		LangChinese:            "启用",
		LangTraditionalChinese: "啟用",
		LangJapanese:           "有効",
		LangSpanish:            "Activar",
	},
	MsgCronBtnDisable: {
		LangEnglish:            "Disable",
		LangChinese:            "暂停",
		LangTraditionalChinese: "暫停",
		LangJapanese:           "無効",
		LangSpanish:            "Desactivar",
	},
	MsgCronBtnMute: {
		LangEnglish:            "Mute",
		LangChinese:            "静音",
		LangTraditionalChinese: "靜音",
		LangJapanese:           "ミュート",
		LangSpanish:            "Silenciar",
	},
	MsgCronBtnUnmute: {
		LangEnglish:            "Unmute",
		LangChinese:            "取消静音",
		LangTraditionalChinese: "取消靜音",
		LangJapanese:           "ミュート解除",
		LangSpanish:            "Reactivar",
	},
	MsgCronBtnDelete: {
		LangEnglish:            "Delete",
		LangChinese:            "删除",
		LangTraditionalChinese: "刪除",
		LangJapanese:           "削除",
		LangSpanish:            "Eliminar",
	},
	MsgCronNextShort: {
		LangEnglish:            "Next",
		LangChinese:            "下次",
		LangTraditionalChinese: "下次",
		LangJapanese:           "次回",
		LangSpanish:            "Prox",
	},
	MsgCronLastShort: {
		LangEnglish:            "Last",
		LangChinese:            "上次",
		LangTraditionalChinese: "上次",
		LangJapanese:           "前回",
		LangSpanish:            "Últ",
	},

	// ── Timer (one-shot) ──────────────────────────────────────

	MsgCardTitleTimer: {
		LangEnglish:            "One-Shot Timer",
		LangChinese:            "一次性定时器",
		LangTraditionalChinese: "一次性定時器",
		LangJapanese:           "ワンショットタイマー",
		LangSpanish:            "Temporizador único",
	},
	MsgTimerNotAvailable: {
		LangEnglish:            "Timer scheduler is not available.",
		LangChinese:            "定时器调度器未启用。",
		LangTraditionalChinese: "定時器調度器未啟用。",
		LangJapanese:           "タイマースケジューラは利用できません。",
		LangSpanish:            "El programador de temporizador no está disponible.",
	},
	MsgTimerUsage: {
		LangEnglish:            "Usage:\n/timer add <delay|time> <prompt>\n/timer addexec <delay|time> <command>\n/timer list\n/timer del <id>\n/timer mute <id> · /timer unmute <id>\n\nDelay: 30m, 2h, 1h30m. Or absolute time: 2026-05-16T09:00\nTime without timezone uses system local time.",
		LangChinese:            "用法：\n/timer add <延迟|时间> <任务描述>\n/timer addexec <延迟|时间> <命令>\n/timer list\n/timer del <id>\n/timer mute <id> · /timer unmute <id>\n\n延迟：30m、2h、1h30m。或绝对时间：2026-05-16T09:00\n不带时区的时间按系统本地时区解析。",
		LangTraditionalChinese: "用法：\n/timer add <延遲|時間> <任務描述>\n/timer addexec <延遲|時間> <命令>\n/timer list\n/timer del <id>\n/timer mute <id> · /timer unmute <id>\n\n延遲：30m、2h、1h30m。或絕對時間：2026-05-16T09:00\n不帶時區的時間按系統本地時區解析。",
		LangJapanese:           "使い方:\n/timer add <遅延|時刻> <タスク内容>\n/timer addexec <遅延|時刻> <コマンド>\n/timer list\n/timer del <id>\n/timer mute <id> · /timer unmute <id>\n\n遅延: 30m, 2h, 1h30m。または絶対時刻: 2026-05-16T09:00\nタイムゾーンなしの時刻はシステムのローカルタイムゾーンで解釈されます。",
		LangSpanish:            "Uso:\n/timer add <retraso|hora> <tarea>\n/timer addexec <retraso|hora> <comando>\n/timer list\n/timer del <id>\n/timer mute <id> · /timer unmute <id>\n\nRetraso: 30m, 2h, 1h30m. O hora absoluta: 2026-05-16T09:00\nHora sin zona horaria usa la hora local del sistema.",
	},
	MsgTimerAddUsage: {
		LangEnglish:            "Usage: /timer add <delay|time> <prompt>\nExamples:\n  /timer add 2h Check PR status\n  /timer add 2026-05-16T09:00 Morning standup reminder\nDelay: 30m, 2h, 1h30m. Time: ISO format (2026-05-16T09:00)\nTime without timezone uses system local time.",
		LangChinese:            "用法：/timer add <延迟|时间> <任务描述>\n示例：\n  /timer add 2h 检查PR状态\n  /timer add 2026-05-16T09:00 早会提醒\n延迟：30m、2h、1h30m。时间：ISO格式（2026-05-16T09:00）\n不带时区的时间按系统本地时区解析。",
		LangTraditionalChinese: "用法：/timer add <延遲|時間> <任務描述>\n範例：\n  /timer add 2h 檢查PR狀態\n  /timer add 2026-05-16T09:00 早會提醒\n延遲：30m、2h、1h30m。時間：ISO格式（2026-05-16T09:00）\n不帶時區的時間按系統本地時區解析。",
		LangJapanese:           "使い方: /timer add <遅延|時刻> <タスク内容>\n例:\n  /timer add 2h PRの状態を確認\n  /timer add 2026-05-16T09:00 朝会リマインダー\n遅延: 30m, 2h, 1h30m。時刻: ISO形式（2026-05-16T09:00）\nタイムゾーンなしの時刻はシステムのローカルタイムゾーンで解釈されます。",
		LangSpanish:            "Uso: /timer add <retraso|hora> <tarea>\nEjemplos:\n  /timer add 2h Verificar estado del PR\n  /timer add 2026-05-16T09:00 Recordatorio de reunión\nRetraso: 30m, 2h, 1h30m. Hora: formato ISO (2026-05-16T09:00)\nHora sin zona horaria usa la hora local del sistema.",
	},
	MsgTimerAdded: {
		LangEnglish:            "⏰ Reminder set (one-shot)\nID: `%s`\nFires in: %s\nPrompt: %s\n(use /timer to view, /cron for recurring tasks)",
		LangChinese:            "⏰ 提醒已设定（一次性）\nID: `%s`\n将在 %s 后触发\n内容: %s\n（用 /timer 查看，周期任务请用 /cron）",
		LangTraditionalChinese: "⏰ 提醒已設定（一次性）\nID: `%s`\n將在 %s 後觸發\n內容: %s\n（用 /timer 查看，週期任務請用 /cron）",
		LangJapanese:           "⏰ リマインダーを設定しました（ワンショット）\nID: `%s`\n%s 後に実行\n内容: %s\n（/timer で確認、繰り返しは /cron）",
		LangSpanish:            "⏰ Recordatorio creado (único)\nID: `%s`\nSe ejecuta en: %s\nContenido: %s\n(use /timer para verlos, /cron para tareas recurrentes)",
	},
	MsgTimerAddedExec: {
		LangEnglish:            "⏰ Shell reminder set (one-shot)\nID: `%s`\nFires in: %s\nCommand: `%s`\n(use /timer to view, /cron for recurring tasks)",
		LangChinese:            "⏰ Shell 提醒已设定（一次性）\nID: `%s`\n将在 %s 后触发\n命令: `%s`\n（用 /timer 查看，周期任务请用 /cron）",
		LangTraditionalChinese: "⏰ Shell 提醒已設定（一次性）\nID: `%s`\n將在 %s 後觸發\n命令: `%s`\n（用 /timer 查看，週期任務請用 /cron）",
		LangJapanese:           "⏰ Shell リマインダーを設定しました（ワンショット）\nID: `%s`\n%s 後に実行\nコマンド: `%s`\n（/timer で確認、繰り返しは /cron）",
		LangSpanish:            "⏰ Recordatorio shell creado (único)\nID: `%s`\nSe ejecuta en: %s\nComando: `%s`\n(use /timer para verlos, /cron para tareas recurrentes)",
	},
	MsgTimerAddExecUsage: {
		LangEnglish:            "Usage: /timer addexec <delay> <shell command>\nExample: /timer addexec 30m df -h",
		LangChinese:            "用法：/timer addexec <延迟> <shell 命令>\n示例：/timer addexec 30m df -h",
		LangTraditionalChinese: "用法：/timer addexec <延遲> <shell 命令>\n範例：/timer addexec 30m df -h",
		LangJapanese:           "使い方: /timer addexec <遅延> <シェルコマンド>\n例: /timer addexec 30m df -h",
		LangSpanish:            "Uso: /timer addexec <retraso> <comando shell>\nEjemplo: /timer addexec 30m df -h",
	},
	MsgTimerEmpty: {
		LangEnglish:            "No pending reminders.\n(For recurring tasks, use /cron)",
		LangChinese:            "暂无待执行的提醒。\n（周期任务请用 /cron 查看）",
		LangTraditionalChinese: "暫無待執行的提醒。\n（週期任務請用 /cron 查看）",
		LangJapanese:           "保留中のリマインダーはありません。\n（繰り返しタスクは /cron をご利用ください）",
		LangSpanish:            "No hay recordatorios pendientes.\n(Para tareas recurrentes use /cron)",
	},
	MsgTimerListTitle: {
		LangEnglish:            "⏰ Pending Timers (%d)",
		LangChinese:            "⏰ 待执行定时器 (%d)",
		LangTraditionalChinese: "⏰ 待執行定時器 (%d)",
		LangJapanese:           "⏰ 保留中のタイマー (%d)",
		LangSpanish:            "⏰ Temporizadores pendientes (%d)",
	},
	MsgTimerListFooter: {
		LangEnglish:            "`/timer del <id>` remove · `/timer mute/unmute <id>` mute",
		LangChinese:            "`/timer del <id>` 删除 · `/timer mute/unmute <id>` 静音",
		LangTraditionalChinese: "`/timer del <id>` 刪除 · `/timer mute/unmute <id>` 靜音",
		LangJapanese:           "`/timer del <id>` 削除 · `/timer mute/unmute <id>` ミュート",
		LangSpanish:            "`/timer del <id>` eliminar · `/timer mute/unmute <id>` silenciar",
	},
	MsgTimerDelUsage: {
		LangEnglish:            "Usage: /timer del <id>",
		LangChinese:            "用法：/timer del <id>",
		LangTraditionalChinese: "用法：/timer del <id>",
		LangJapanese:           "使い方: /timer del <id>",
		LangSpanish:            "Uso: /timer del <id>",
	},
	MsgTimerMuteUsage: {
		LangEnglish:            "Usage: /timer mute <id> · /timer unmute <id>",
		LangChinese:            "用法：/timer mute <id> · /timer unmute <id>",
		LangTraditionalChinese: "用法：/timer mute <id> · /timer unmute <id>",
		LangJapanese:           "使い方: /timer mute <id> · /timer unmute <id>",
		LangSpanish:            "Uso: /timer mute <id> · /timer unmute <id>",
	},
	MsgTimerDeleted: {
		LangEnglish:            "✅ Timer `%s` cancelled.",
		LangChinese:            "✅ 定时器 `%s` 已取消。",
		LangTraditionalChinese: "✅ 定時器 `%s` 已取消。",
		LangJapanese:           "✅ タイマー `%s` をキャンセルしました。",
		LangSpanish:            "✅ Temporizador `%s` cancelado.",
	},
	MsgTimerNotFound: {
		LangEnglish:            "❌ Timer `%s` not found.",
		LangChinese:            "❌ 定时器 `%s` 未找到。",
		LangTraditionalChinese: "❌ 定時器 `%s` 未找到。",
		LangJapanese:           "❌ タイマー `%s` が見つかりません。",
		LangSpanish:            "❌ Temporizador `%s` no encontrado.",
	},
	MsgTimerMuted: {
		LangEnglish:            "🔇 Timer `%s` muted.",
		LangChinese:            "🔇 定时器 `%s` 已静音。",
		LangTraditionalChinese: "🔇 定時器 `%s` 已靜音。",
		LangJapanese:           "🔇 タイマー `%s` をミュートしました。",
		LangSpanish:            "🔇 Temporizador `%s` silenciado.",
	},
	MsgTimerUnmuted: {
		LangEnglish:            "🔔 Timer `%s` unmuted.",
		LangChinese:            "🔔 定时器 `%s` 已取消静音。",
		LangTraditionalChinese: "🔔 定時器 `%s` 已取消靜音。",
		LangJapanese:           "🔔 タイマー `%s` のミュートを解除しました。",
		LangSpanish:            "🔔 Temporizador `%s` reactivado.",
	},
	MsgTimerCardHint: {
		LangEnglish:            "💡 `/timer add <delay> <prompt>` · `/timer del <id>` · `/timer mute/unmute <id>`",
		LangChinese:            "💡 `/timer add <延迟> <内容>` 添加 · `/timer del <id>` 删除 · `/timer mute/unmute <id>` 静音",
		LangTraditionalChinese: "💡 `/timer add <延遲> <內容>` 新增 · `/timer del <id>` 刪除 · `/timer mute/unmute <id>` 靜音",
		LangJapanese:           "💡 `/timer add <遅延> <内容>` 追加 · `/timer del <id>` 削除 · `/timer mute/unmute <id>` ミュート",
		LangSpanish:            "💡 `/timer add <retraso> <tarea>` · `/timer del <id>` · `/timer mute/unmute <id>`",
	},
	MsgTimerBtnMute: {
		LangEnglish:            "Mute",
		LangChinese:            "静音",
		LangTraditionalChinese: "靜音",
		LangJapanese:           "ミュート",
		LangSpanish:            "Silenciar",
	},
	MsgTimerBtnUnmute: {
		LangEnglish:            "Unmute",
		LangChinese:            "取消静音",
		LangTraditionalChinese: "取消靜音",
		LangJapanese:           "ミュート解除",
		LangSpanish:            "Reactivar",
	},
	MsgTimerBtnDelete: {
		LangEnglish:            "Cancel Timer",
		LangChinese:            "取消定时器",
		LangTraditionalChinese: "取消定時器",
		LangJapanese:           "タイマーをキャンセル",
		LangSpanish:            "Cancelar temporizador",
	},
	MsgTimerIDLabel: {
		LangEnglish: "ID: %s\n", LangChinese: "ID：%s\n", LangTraditionalChinese: "ID：%s\n",
		LangJapanese: "ID: %s\n", LangSpanish: "ID: %s\n",
	},
	MsgTimerScheduledLabel: {
		LangEnglish:            "Scheduled: %s (%s remaining)\n",
		LangChinese:            "计划: %s（剩余 %s）\n",
		LangTraditionalChinese: "計劃: %s（剩餘 %s）\n",
		LangJapanese:           "予定: %s（残り %s）\n",
		LangSpanish:            "Programado: %s (%s restante)\n",
	},
	MsgTimerFailedSuffix: {
		LangEnglish: " (failed: %s)", LangChinese: "（失败：%s）", LangTraditionalChinese: "（失敗：%s）",
		LangJapanese: "（失敗: %s）", LangSpanish: " (falló: %s)",
	},

	MsgStatusTitle: {
		LangEnglish: "lark-agent-bot Status\n\n" +
			"Project: %s\n" +
			"Agent: %s\n" +
			"Work Dir: %s\n" +
			"Platforms: %s\n" +
			"Uptime: %s\n" +
			"Language: %s\n" +
			"%s" + "%s" + "%s" + "%s" + "%s" + "%s",
		LangChinese: "lark-agent-bot 状态\n\n" +
			"项目: %s\n" +
			"Agent: %s\n" +
			"工作目录: %s\n" +
			"平台: %s\n" +
			"运行时间: %s\n" +
			"语言: %s\n" +
			"%s" + "%s" + "%s" + "%s" + "%s" + "%s",
		LangTraditionalChinese: "lark-agent-bot 狀態\n\n" +
			"項目: %s\n" +
			"Agent: %s\n" +
			"工作目錄: %s\n" +
			"平台: %s\n" +
			"運行時間: %s\n" +
			"語言: %s\n" +
			"%s" + "%s" + "%s" + "%s" + "%s" + "%s",
		LangJapanese: "lark-agent-bot ステータス\n\n" +
			"プロジェクト: %s\n" +
			"エージェント: %s\n" +
			"作業ディレクトリ: %s\n" +
			"プラットフォーム: %s\n" +
			"稼働時間: %s\n" +
			"言語: %s\n" +
			"%s" + "%s" + "%s" + "%s" + "%s" + "%s",
		LangSpanish: "Estado de lark-agent-bot\n\n" +
			"Proyecto: %s\n" +
			"Agente: %s\n" +
			"Directorio: %s\n" +
			"Plataformas: %s\n" +
			"Tiempo activo: %s\n" +
			"Idioma: %s\n" +
			"%s" + "%s" + "%s" + "%s" + "%s" + "%s",
	},
	MsgReplyFooterRemaining: {
		LangEnglish:            "%d%% left",
		LangChinese:            "剩余 %d%%",
		LangTraditionalChinese: "剩餘 %d%%",
		LangJapanese:           "残り %d%%",
		LangSpanish:            "%d%% restante",
	},
	MsgReplyFooterQuota5h: {
		LangEnglish:            "5h %d%% used",
		LangChinese:            "5小时已用 %d%%",
		LangTraditionalChinese: "5小時已用 %d%%",
		LangJapanese:           "5時間 %d%% 使用",
		LangSpanish:            "5h %d%% usado",
	},
	MsgReplyFooterQuotaWeek: {
		LangEnglish:            "week %d%% used",
		LangChinese:            "本周已用 %d%%",
		LangTraditionalChinese: "本週已用 %d%%",
		LangJapanese:           "週 %d%% 使用",
		LangSpanish:            "semana %d%% usado",
	},
	MsgModelCurrent: {
		LangEnglish:            "Current model: %s",
		LangChinese:            "当前模型: %s",
		LangTraditionalChinese: "當前模型: %s",
		LangJapanese:           "現在のモデル: %s",
		LangSpanish:            "Modelo actual: %s",
	},
	MsgModelChanged: {
		LangEnglish:            "Model switched to `%s`. This session and all future sessions will use it.",
		LangChinese:            "模型已切换为 `%s`，当前会话与后续会话均使用此模型。",
		LangTraditionalChinese: "模型已切換為 `%s`，當前會話與後續會話均使用此模型。",
		LangJapanese:           "モデルを `%s` に切り替えました。このセッションと今後のセッションで使用されます。",
		LangSpanish:            "Modelo cambiado a `%s`. Esta sesión y las futuras usarán este modelo.",
	},
	MsgModelChangeFailed: {
		LangEnglish:            "❌ Failed to change model: %v",
		LangChinese:            "❌ 切换模型失败: %v",
		LangTraditionalChinese: "❌ 切換模型失敗: %v",
		LangJapanese:           "❌ モデルの切り替えに失敗しました: %v",
		LangSpanish:            "❌ Error al cambiar el modelo: %v",
	},
	MsgModelCardSwitching: {
		LangEnglish:            "Switching model to `%s`...",
		LangChinese:            "正在切换模型为 `%s`...",
		LangTraditionalChinese: "正在切換模型為 `%s`...",
		LangJapanese:           "モデルを `%s` に切り替えています...",
		LangSpanish:            "Cambiando el modelo a `%s`...",
	},
	MsgModelCardSwitched: {
		LangEnglish:            "Model switched to `%s`.",
		LangChinese:            "模型已切换为 `%s`。",
		LangTraditionalChinese: "模型已切換為 `%s`。",
		LangJapanese:           "モデルを `%s` に切り替えました。",
		LangSpanish:            "Modelo cambiado a `%s`.",
	},
	MsgModelCardSwitchFailed: {
		LangEnglish:            "Failed to switch model: %v",
		LangChinese:            "切换模型失败: %v",
		LangTraditionalChinese: "切換模型失敗: %v",
		LangJapanese:           "モデルの切り替えに失敗しました: %v",
		LangSpanish:            "Error al cambiar el modelo: %v",
	},
	MsgModelNotSupported: {
		LangEnglish:            "This agent does not support model switching.",
		LangChinese:            "当前 Agent 不支持模型切换。",
		LangTraditionalChinese: "當前 Agent 不支援模型切換。",
		LangJapanese:           "このエージェントはモデルの切り替えをサポートしていません。",
		LangSpanish:            "Este agente no soporta el cambio de modelo.",
	},
	MsgReasoningCurrent: {
		LangEnglish:            "Current reasoning effort: %s",
		LangChinese:            "当前推理强度: %s",
		LangTraditionalChinese: "當前推理強度: %s",
		LangJapanese:           "現在の推論強度: %s",
		LangSpanish:            "Esfuerzo de razonamiento actual: %s",
	},
	MsgTurnStoppedBySettingChange: {
		LangEnglish:            "⚠️ Changing the setting stopped the task that was running before it finished. Send your message again if you still need it.",
		LangChinese:            "⚠️ 修改设置中断了正在进行的任务，它没有完成。需要的话请重新发送。",
		LangTraditionalChinese: "⚠️ 修改設定中斷了正在進行的任務，它沒有完成。需要的話請重新傳送。",
		LangJapanese:           "⚠️ 設定の変更により実行中のタスクが完了前に中断されました。必要であればもう一度送信してください。",
		LangSpanish:            "⚠️ El cambio de configuración detuvo la tarea en curso antes de que terminara. Vuelve a enviar tu mensaje si aún lo necesitas.",
	},
	MsgResumeFailedNewSession: {
		LangEnglish:            "⚠️ Could not continue the previous conversation (%s), so this message started a new one without its earlier context.",
		LangChinese:            "⚠️ 无法接续之前的会话（%s），这条消息已在新会话中处理，之前的上下文没有带过来。",
		LangTraditionalChinese: "⚠️ 無法接續之前的會話（%s），這則訊息已在新會話中處理，之前的上下文沒有帶過來。",
		LangJapanese:           "⚠️ 以前の会話（%s）を再開できなかったため、このメッセージは以前の文脈なしの新しい会話で処理されました。",
		LangSpanish:            "⚠️ No se pudo continuar la conversación anterior (%s), así que este mensaje inició una nueva sin su contexto previo.",
	},
	MsgReasoningChanged: {
		LangEnglish:            "Reasoning effort switched to `%s`. It applies from your next message; the conversation continues.",
		LangChinese:            "推理强度已切换为 `%s`，从下一条消息开始生效，会话保持不变。",
		LangTraditionalChinese: "推理強度已切換為 `%s`，從下一則訊息開始生效，會話保持不變。",
		LangJapanese:           "推論強度を `%s` に切り替えました。次のメッセージから適用され、会話はそのまま続きます。",
		LangSpanish:            "Esfuerzo de razonamiento cambiado a `%s`. Se aplica desde tu próximo mensaje y la conversación continúa.",
	},
	MsgReasoningNotSupported: {
		LangEnglish:            "This agent does not support reasoning effort switching.",
		LangChinese:            "当前 Agent 不支持推理强度切换。",
		LangTraditionalChinese: "當前 Agent 不支援推理強度切換。",
		LangJapanese:           "このエージェントは推論強度の切り替えをサポートしていません。",
		LangSpanish:            "Este agente no soporta el cambio de esfuerzo de razonamiento.",
	},
	MsgMemoryNotSupported: {
		LangEnglish:            "This agent does not support memory files.",
		LangChinese:            "当前 Agent 不支持记忆文件。",
		LangTraditionalChinese: "當前 Agent 不支援記憶檔案。",
		LangJapanese:           "このエージェントはメモリファイルをサポートしていません。",
		LangSpanish:            "Este agente no soporta archivos de memoria.",
	},
	MsgMemoryShowProject: {
		LangEnglish:            "📝 **Project Memory** (`%s`)\n\n%s",
		LangChinese:            "📝 **项目记忆** (`%s`)\n\n%s",
		LangTraditionalChinese: "📝 **項目記憶** (`%s`)\n\n%s",
		LangJapanese:           "📝 **プロジェクトメモリ** (`%s`)\n\n%s",
		LangSpanish:            "📝 **Memoria del proyecto** (`%s`)\n\n%s",
	},
	MsgMemoryShowGlobal: {
		LangEnglish:            "📝 **Global Memory** (`%s`)\n\n%s",
		LangChinese:            "📝 **全局记忆** (`%s`)\n\n%s",
		LangTraditionalChinese: "📝 **全域記憶** (`%s`)\n\n%s",
		LangJapanese:           "📝 **グローバルメモリ** (`%s`)\n\n%s",
		LangSpanish:            "📝 **Memoria global** (`%s`)\n\n%s",
	},
	MsgMemoryEmpty: {
		LangEnglish:            "📝 `%s`\n\n(empty — no content yet)",
		LangChinese:            "📝 `%s`\n\n（空 — 尚无内容）",
		LangTraditionalChinese: "📝 `%s`\n\n（空 — 尚無內容）",
		LangJapanese:           "📝 `%s`\n\n（空 — まだ内容がありません）",
		LangSpanish:            "📝 `%s`\n\n(vacío — aún sin contenido)",
	},
	MsgMemoryAdded: {
		LangEnglish:            "✅ Added to `%s`",
		LangChinese:            "✅ 已追加到 `%s`",
		LangTraditionalChinese: "✅ 已追加到 `%s`",
		LangJapanese:           "✅ `%s` に追加しました",
		LangSpanish:            "✅ Agregado a `%s`",
	},
	MsgMemoryAddFailed: {
		LangEnglish:            "❌ Failed to write memory file: %v",
		LangChinese:            "❌ 写入记忆文件失败: %v",
		LangTraditionalChinese: "❌ 寫入記憶檔案失敗: %v",
		LangJapanese:           "❌ メモリファイルの書き込みに失敗しました: %v",
		LangSpanish:            "❌ Error al escribir archivo de memoria: %v",
	},
	MsgUsageNotSupported: {
		LangEnglish:            "Current agent does not support `/usage`.",
		LangChinese:            "当前 Agent 不支持 `/usage`。",
		LangTraditionalChinese: "目前 Agent 不支援 `/usage`。",
		LangJapanese:           "現在のエージェントは `/usage` をサポートしていません。",
		LangSpanish:            "El agente actual no admite `/usage`.",
	},
	MsgUsageFetchFailed: {
		LangEnglish:            "Failed to fetch usage: %v",
		LangChinese:            "获取 usage 失败：%v",
		LangTraditionalChinese: "取得 usage 失敗：%v",
		LangJapanese:           "usage の取得に失敗しました: %v",
		LangSpanish:            "No se pudo obtener usage: %v",
	},
	MsgMemoryAddUsage: {
		LangEnglish: "Usage:\n" +
			"`/memory` — show project memory\n" +
			"`/memory add <text>` — add to project memory\n" +
			"`/memory global` — show global memory\n" +
			"`/memory global add <text>` — add to global memory",
		LangChinese: "用法：\n" +
			"`/memory` — 查看项目记忆\n" +
			"`/memory add <文本>` — 追加到项目记忆\n" +
			"`/memory global` — 查看全局记忆\n" +
			"`/memory global add <文本>` — 追加到全局记忆",
		LangTraditionalChinese: "用法：\n" +
			"`/memory` — 查看項目記憶\n" +
			"`/memory add <文字>` — 追加到項目記憶\n" +
			"`/memory global` — 查看全域記憶\n" +
			"`/memory global add <文字>` — 追加到全域記憶",
		LangJapanese: "使い方:\n" +
			"`/memory` — プロジェクトメモリを表示\n" +
			"`/memory add <テキスト>` — プロジェクトメモリに追加\n" +
			"`/memory global` — グローバルメモリを表示\n" +
			"`/memory global add <テキスト>` — グローバルメモリに追加",
		LangSpanish: "Uso:\n" +
			"`/memory` — ver memoria del proyecto\n" +
			"`/memory add <texto>` — agregar a memoria del proyecto\n" +
			"`/memory global` — ver memoria global\n" +
			"`/memory global add <texto>` — agregar a memoria global",
	},
	MsgCompressNotSupported: {
		LangEnglish:            "This agent does not support context compression.",
		LangChinese:            "当前 Agent 不支持上下文压缩。可以使用 `/new` 开始新会话。",
		LangTraditionalChinese: "當前 Agent 不支援上下文壓縮。可以使用 `/new` 開始新會話。",
		LangJapanese:           "このエージェントはコンテキスト圧縮をサポートしていません。`/new` で新しいセッションを開始できます。",
		LangSpanish:            "Este agente no soporta la compresión de contexto. Puede usar `/new` para iniciar una nueva sesión.",
	},
	MsgCompressing: {
		LangEnglish:            "🗜 Compressing context...",
		LangChinese:            "🗜 正在压缩上下文...",
		LangTraditionalChinese: "🗜 正在壓縮上下文...",
		LangJapanese:           "🗜 コンテキストを圧縮中...",
		LangSpanish:            "🗜 Comprimiendo contexto...",
	},
	MsgCompressNoSession: {
		LangEnglish:            "No active session to compress. Send a message first.",
		LangChinese:            "没有活跃的会话可以压缩。请先发送一条消息。",
		LangTraditionalChinese: "沒有活躍的會話可以壓縮。請先發送一條訊息。",
		LangJapanese:           "圧縮するアクティブなセッションがありません。まずメッセージを送信してください。",
		LangSpanish:            "No hay sesión activa para comprimir. Envíe un mensaje primero.",
	},
	MsgCompressDone: {
		LangEnglish:            "✅ Context compressed.",
		LangChinese:            "✅ 上下文压缩完成。",
		LangTraditionalChinese: "✅ 上下文壓縮完成。",
		LangJapanese:           "✅ コンテキスト圧縮完了。",
		LangSpanish:            "✅ Contexto comprimido.",
	},

	// Inline strings for engine.go commands
	MsgStatusMode: {
		LangEnglish:            "Mode: %s\n",
		LangChinese:            "权限模式: %s\n",
		LangTraditionalChinese: "權限模式: %s\n",
		LangJapanese:           "権限モード: %s\n",
		LangSpanish:            "Modo: %s\n",
	},
	MsgStatusSession: {
		LangEnglish:            "Session: %s (messages: %d)\n",
		LangChinese:            "当前会话: %s (消息: %d)\n",
		LangTraditionalChinese: "當前會話: %s (訊息: %d)\n",
		LangJapanese:           "セッション: %s (メッセージ: %d)\n",
		LangSpanish:            "Sesión: %s (mensajes: %d)\n",
	},
	MsgStatusCron: {
		LangEnglish:            "Cron jobs: %d (enabled: %d)\n",
		LangChinese:            "定时任务: %d (启用: %d)\n",
		LangTraditionalChinese: "定時任務: %d (啟用: %d)\n",
		LangJapanese:           "スケジュールタスク: %d (有効: %d)\n",
		LangSpanish:            "Tareas programadas: %d (habilitadas: %d)\n",
	},
	MsgStatusThinkingMessages: {
		LangEnglish:            "Thinking messages: %s\n",
		LangChinese:            "思考消息: %s\n",
		LangTraditionalChinese: "思考訊息: %s\n",
		LangJapanese:           "思考メッセージ: %s\n",
		LangSpanish:            "Mensajes de razonamiento: %s\n",
	},
	MsgStatusToolMessages: {
		LangEnglish:            "Tool progress: %s\n",
		LangChinese:            "工具进度: %s\n",
		LangTraditionalChinese: "工具進度: %s\n",
		LangJapanese:           "ツール進捗: %s\n",
		LangSpanish:            "Progreso de herramientas: %s\n",
	},
	MsgStatusSessionKey: {
		LangEnglish:            "Session Key: `%s`\n",
		LangChinese:            "会话 Key: `%s`\n",
		LangTraditionalChinese: "會話 Key: `%s`\n",
		LangJapanese:           "セッションキー: `%s`\n",
		LangSpanish:            "Clave de sesión: `%s`\n",
	},
	MsgStatusAgentSID: {
		LangEnglish:            "Agent SID: `%s`\n",
		LangChinese:            "Agent SID: `%s`\n",
		LangTraditionalChinese: "Agent SID: `%s`\n",
		LangJapanese:           "Agent SID: `%s`\n",
		LangSpanish:            "Agent SID: `%s`\n",
	},
	MsgStatusUserID: {
		LangEnglish:            "User ID: `%s`\n",
		LangChinese:            "User ID: `%s`\n",
		LangTraditionalChinese: "User ID: `%s`\n",
		LangJapanese:           "ユーザーID: `%s`\n",
		LangSpanish:            "ID de usuario: `%s`\n",
	},
	MsgEnabledShort: {
		LangEnglish:            "ON",
		LangChinese:            "开启",
		LangTraditionalChinese: "開啟",
		LangJapanese:           "ON",
		LangSpanish:            "Activado",
	},
	MsgDisabledShort: {
		LangEnglish:            "OFF",
		LangChinese:            "关闭",
		LangTraditionalChinese: "關閉",
		LangJapanese:           "OFF",
		LangSpanish:            "Desactivado",
	},
	MsgModelDefault: {
		LangEnglish:            "Current model: (not set, using agent default)\n",
		LangChinese:            "当前模型: (未设置，使用 Agent 默认值)\n",
		LangTraditionalChinese: "當前模型: (未設置，使用 Agent 預設值)\n",
		LangJapanese:           "現在のモデル: (未設定、エージェントのデフォルトを使用)\n",
		LangSpanish:            "Modelo actual: (no configurado, usando predeterminado del agente)\n",
	},
	MsgModelListTitle: {
		LangEnglish:            "Available models:\n",
		LangChinese:            "可用模型:\n",
		LangTraditionalChinese: "可用模型:\n",
		LangJapanese:           "利用可能なモデル:\n",
		LangSpanish:            "Modelos disponibles:\n",
	},
	MsgModelUsage: {
		LangEnglish:            "Usage: `/model switch <number>` or `/model switch <model_name>`",
		LangChinese:            "用法: `/model switch <序号>` 或 `/model switch <模型名>`",
		LangTraditionalChinese: "用法: `/model switch <序號>` 或 `/model switch <模型名>`",
		LangJapanese:           "使い方: `/model switch <番号>` または `/model switch <モデル名>`",
		LangSpanish:            "Uso: `/model switch <número>` o `/model switch <nombre_modelo>`",
	},
	MsgReasoningDefault: {
		LangEnglish:            "Current reasoning effort: (not set, using the Agent's own setting or default)\n",
		LangChinese:            "当前推理强度: (未设置，使用 Agent 自身的设置或默认值)\n",
		LangTraditionalChinese: "當前推理強度: (未設置，使用 Agent 自身的設定或預設值)\n",
		LangJapanese:           "現在の推論強度: (未設定、Agent 自身の設定またはデフォルトを使用)\n",
		LangSpanish:            "Esfuerzo de razonamiento actual: (no configurado, usando la configuración propia del Agent o su valor predeterminado)\n",
	},
	MsgReasoningListTitle: {
		LangEnglish:            "Available reasoning levels:\n",
		LangChinese:            "可用推理强度:\n",
		LangTraditionalChinese: "可用推理強度:\n",
		LangJapanese:           "利用可能な推論強度:\n",
		LangSpanish:            "Niveles de razonamiento disponibles:\n",
	},
	MsgReasoningUsage: {
		LangEnglish:            "Usage: `/reasoning <number>` or `/reasoning <%s>`",
		LangChinese:            "用法: `/reasoning <序号>` 或 `/reasoning <%s>`",
		LangTraditionalChinese: "用法: `/reasoning <序號>` 或 `/reasoning <%s>`",
		LangJapanese:           "使い方: `/reasoning <番号>` または `/reasoning <%s>`",
		LangSpanish:            "Uso: `/reasoning <número>` o `/reasoning <%s>`",
	},
	MsgModeUsage: {
		LangEnglish:            "\nUse `/mode <name>` to switch.\nAvailable: %s",
		LangChinese:            "\n使用 `/mode <名称>` 切换模式\n可用值: %s",
		LangTraditionalChinese: "\n使用 `/mode <名稱>` 切換模式\n可用值: %s",
		LangJapanese:           "\n`/mode <名前>` で切り替え\n選択肢: %s",
		LangSpanish:            "\nUse `/mode <nombre>` para cambiar.\nDisponibles: %s",
	},
	MsgLangSelectPlaceholder: {
		LangEnglish: "Select language", LangChinese: "选择语言", LangTraditionalChinese: "選擇語言",
		LangJapanese: "言語を選択", LangSpanish: "Seleccionar idioma",
	},
	MsgModelSelectPlaceholder: {
		LangEnglish: "Select model", LangChinese: "选择模型", LangTraditionalChinese: "選擇模型",
		LangJapanese: "モデルを選択", LangSpanish: "Seleccionar modelo",
	},
	MsgReasoningSelectPlaceholder: {
		LangEnglish: "Select reasoning level", LangChinese: "选择推理强度", LangTraditionalChinese: "選擇推理強度",
		LangJapanese: "推論強度を選択", LangSpanish: "Seleccionar nivel de razonamiento",
	},
	MsgModeSelectPlaceholder: {
		LangEnglish: "Select mode", LangChinese: "选择模式", LangTraditionalChinese: "選擇模式",
		LangJapanese: "モードを選択", LangSpanish: "Seleccionar modo",
	},
	MsgProviderSelectPlaceholder: {
		LangEnglish: "Select provider", LangChinese: "选择 Provider", LangTraditionalChinese: "選擇 Provider",
		LangJapanese: "プロバイダーを選択", LangSpanish: "Seleccionar proveedor",
	},
	MsgProviderClearOption: {
		LangEnglish: "Do not use provider", LangChinese: "不使用服务商", LangTraditionalChinese: "不使用服務商",
		LangJapanese: "プロバイダーを使用しない", LangSpanish: "No usar proveedor",
	},
	MsgCardBack: {
		LangEnglish: "← Back", LangChinese: "← 返回", LangTraditionalChinese: "← 返回",
		LangJapanese: "← 戻る", LangSpanish: "← Volver",
	},
	MsgCardPrev: {
		LangEnglish: "← Prev", LangChinese: "← 上一页", LangTraditionalChinese: "← 上一頁",
		LangJapanese: "← 前へ", LangSpanish: "← Anterior",
	},
	MsgCardNext: {
		LangEnglish: "Next →", LangChinese: "下一页 →", LangTraditionalChinese: "下一頁 →",
		LangJapanese: "次へ →", LangSpanish: "Siguiente →",
	},
	MsgCardTitleStatus: {
		LangEnglish: "lark-agent-bot Status", LangChinese: "lark-agent-bot 状态", LangTraditionalChinese: "lark-agent-bot 狀態",
		LangJapanese: "lark-agent-bot ステータス", LangSpanish: "Estado de lark-agent-bot",
	},
	MsgCardTitleLanguage: {
		LangEnglish: "Language", LangChinese: "语言", LangTraditionalChinese: "語言",
		LangJapanese: "言語", LangSpanish: "Idioma",
	},
	MsgCardTitleModel: {
		LangEnglish: "Model", LangChinese: "模型", LangTraditionalChinese: "模型",
		LangJapanese: "モデル", LangSpanish: "Modelo",
	},
	MsgCardTitleReasoning: {
		LangEnglish: "Reasoning", LangChinese: "推理强度", LangTraditionalChinese: "推理強度",
		LangJapanese: "推論強度", LangSpanish: "Razonamiento",
	},
	MsgCardTitleMode: {
		LangEnglish: "Permission Mode", LangChinese: "权限模式", LangTraditionalChinese: "權限模式",
		LangJapanese: "権限モード", LangSpanish: "Modo de permisos",
	},
	MsgCardTitleSessions: {
		LangEnglish: "%s Sessions (%d)", LangChinese: "%s 会话列表 (%d)", LangTraditionalChinese: "%s 會話列表 (%d)",
		LangJapanese: "%s セッション (%d)", LangSpanish: "Sesiones de %s (%d)",
	},
	MsgCardTitleSessionsPaged: {
		LangEnglish: "%s Sessions (%d) — %d/%d", LangChinese: "%s 会话列表 (%d) · 第 %d/%d 页", LangTraditionalChinese: "%s 會話列表 (%d) · 第 %d/%d 頁",
		LangJapanese: "%s セッション (%d) · %d/%d ページ", LangSpanish: "Sesiones de %s (%d) · Página %d/%d",
	},
	MsgCardTitleCurrentSession: {
		LangEnglish: "Current Session", LangChinese: "当前会话", LangTraditionalChinese: "當前會話",
		LangJapanese: "現在のセッション", LangSpanish: "Sesión actual",
	},
	MsgCardTitleHistory: {
		LangEnglish: "History", LangChinese: "历史记录", LangTraditionalChinese: "歷史記錄",
		LangJapanese: "履歴", LangSpanish: "Historial",
	},
	MsgCardTitleHistoryLast: {
		LangEnglish: "History (last %d)", LangChinese: "历史记录（最近 %d 条）", LangTraditionalChinese: "歷史記錄（最近 %d 條）",
		LangJapanese: "履歴（直近 %d 件）", LangSpanish: "Historial (últimos %d)",
	},
	MsgCardTitleProvider: {
		LangEnglish: "Provider", LangChinese: "Provider", LangTraditionalChinese: "Provider",
		LangJapanese: "プロバイダー", LangSpanish: "Proveedor",
	},
	MsgCardTitleCron: {
		LangEnglish: "Cron", LangChinese: "定时任务", LangTraditionalChinese: "定時任務",
		LangJapanese: "スケジュールタスク", LangSpanish: "Tareas programadas",
	},
	MsgCardTitleHeartbeat: {
		LangEnglish: "Heartbeat", LangChinese: "心跳", LangTraditionalChinese: "心跳",
		LangJapanese: "ハートビート", LangSpanish: "Heartbeat",
	},
	MsgCardTitleCommands: {
		LangEnglish: "Commands", LangChinese: "命令", LangTraditionalChinese: "命令",
		LangJapanese: "コマンド", LangSpanish: "Comandos",
	},
	MsgCardTitleAlias: {
		LangEnglish: "Alias", LangChinese: "别名", LangTraditionalChinese: "別名",
		LangJapanese: "エイリアス", LangSpanish: "Alias",
	},
	MsgCardTitleConfig: {
		LangEnglish: "Config", LangChinese: "配置", LangTraditionalChinese: "配置",
		LangJapanese: "設定", LangSpanish: "Configuración",
	},
	MsgCardTitleSkills: {
		LangEnglish: "Skills", LangChinese: "Skills", LangTraditionalChinese: "Skills",
		LangJapanese: "スキル", LangSpanish: "Skills",
	},
	MsgCardTitleDoctor: {
		LangEnglish: "Doctor", LangChinese: "系统诊断", LangTraditionalChinese: "系統診斷",
		LangJapanese: "診断", LangSpanish: "Diagnóstico",
	},
	MsgCardTitleVersion: {
		LangEnglish: "Version", LangChinese: "版本", LangTraditionalChinese: "版本",
		LangJapanese: "バージョン", LangSpanish: "Versión",
	},
	MsgCardTitleUpgrade: {
		LangEnglish: "Upgrade", LangChinese: "升级", LangTraditionalChinese: "升級",
		LangJapanese: "アップグレード", LangSpanish: "Actualización",
	},
	MsgListItem: {
		LangEnglish:            "%s **%d.** %s · **%d** msgs · %s",
		LangChinese:            "%s **%d.** %s · **%d** 条消息 · %s",
		LangTraditionalChinese: "%s **%d.** %s · **%d** 則訊息 · %s",
		LangJapanese:           "%s **%d.** %s · **%d** 件のメッセージ · %s",
		LangSpanish:            "%s **%d.** %s · **%d** mensajes · %s",
	},
	MsgListEmptySummary: {
		LangEnglish: "(empty)", LangChinese: "（空）", LangTraditionalChinese: "（空）",
		LangJapanese: "（空）", LangSpanish: "(vacío)",
	},
	MsgCronIDLabel: {
		LangEnglish: "ID: %s\n", LangChinese: "ID：%s\n", LangTraditionalChinese: "ID：%s\n",
		LangJapanese: "ID: %s\n", LangSpanish: "ID: %s\n",
	},
	MsgCronFailedSuffix: {
		LangEnglish: " (failed: %s)", LangChinese: "（失败：%s）", LangTraditionalChinese: "（失敗：%s）",
		LangJapanese: "（失敗: %s）", LangSpanish: " (falló: %s)",
	},
	MsgCommandsTagAgent: {
		LangEnglish: " [agent]", LangChinese: " [代理]", LangTraditionalChinese: " [代理]",
		LangJapanese: " [エージェント]", LangSpanish: " [agente]",
	},
	MsgCommandsTagShell: {
		LangEnglish: " [shell]", LangChinese: " [终端]", LangTraditionalChinese: " [終端]",
		LangJapanese: " [シェル]", LangSpanish: " [shell]",
	},
	MsgUpgradeTimeoutSuffix: {
		LangEnglish: " (timeout)", LangChinese: "（超时）", LangTraditionalChinese: "（逾時）",
		LangJapanese: "（タイムアウト）", LangSpanish: " (tiempo de espera agotado)",
	},
	MsgCronScheduleLabel: {
		LangEnglish:            "Schedule: %s `%s`\n",
		LangChinese:            "调度: %s `%s`\n",
		LangTraditionalChinese: "調度: %s `%s`\n",
		LangJapanese:           "スケジュール: %s `%s`\n",
		LangSpanish:            "Programación: %s `%s`\n",
	},
	MsgCronNextRunLabel: {
		LangEnglish:            "Next run: %s\n",
		LangChinese:            "下次执行: %s\n",
		LangTraditionalChinese: "下次執行: %s\n",
		LangJapanese:           "次回実行: %s\n",
		LangSpanish:            "Próxima ejecución: %s\n",
	},
	MsgCronLastRunLabel: {
		LangEnglish:            "Last run: %s",
		LangChinese:            "上次执行: %s",
		LangTraditionalChinese: "上次執行: %s",
		LangJapanese:           "前回実行: %s",
		LangSpanish:            "Última ejecución: %s",
	},
	MsgPermBtnAllow: {
		LangEnglish:            "Allow",
		LangChinese:            "允许",
		LangTraditionalChinese: "允許",
		LangJapanese:           "許可",
		LangSpanish:            "Permitir",
	},
	MsgPermBtnDeny: {
		LangEnglish:            "Deny",
		LangChinese:            "拒绝",
		LangTraditionalChinese: "拒絕",
		LangJapanese:           "拒否",
		LangSpanish:            "Denegar",
	},
	MsgPermBtnAllowAll: {
		LangEnglish:            "Allow All (this session)",
		LangChinese:            "允许所有 (本次会话)",
		LangTraditionalChinese: "允許所有 (本次會話)",
		LangJapanese:           "すべて許可 (このセッション)",
		LangSpanish:            "Permitir todo (esta sesión)",
	},
	MsgPermCardTitle: {
		LangEnglish:            "Permission Request",
		LangChinese:            "权限请求",
		LangTraditionalChinese: "權限請求",
		LangJapanese:           "権限リクエスト",
		LangSpanish:            "Solicitud de permiso",
	},
	MsgPermCardBody: {
		LangEnglish:            "Agent wants to use **%s**:\n\n```\n%s\n```",
		LangChinese:            "Agent 想要使用 **%s**:\n\n```\n%s\n```",
		LangTraditionalChinese: "Agent 想要使用 **%s**:\n\n```\n%s\n```",
		LangJapanese:           "エージェントが **%s** を使用しようとしています:\n\n```\n%s\n```",
		LangSpanish:            "El agente quiere usar **%s**:\n\n```\n%s\n```",
	},
	MsgPermCardNote: {
		LangEnglish:            "If buttons are unresponsive, reply: allow / deny / allow all",
		LangChinese:            "如果按钮无响应，请直接回复：允许 / 拒绝 / 允许所有",
		LangTraditionalChinese: "若按鈕無回應，請直接回覆：允許 / 拒絕 / 允許所有",
		LangJapanese:           "ボタンが反応しない場合は直接返信: allow / deny / allow all",
		LangSpanish:            "Si los botones no responden, responda: allow / deny / allow all",
	},
	MsgAskQuestionTitle: {
		LangEnglish:            "Agent Question",
		LangChinese:            "Agent 提问",
		LangTraditionalChinese: "Agent 提問",
		LangJapanese:           "エージェントの質問",
		LangSpanish:            "Pregunta del agente",
	},
	MsgAskQuestionNote: {
		LangEnglish:            "If buttons are unresponsive, reply with the option number (e.g. 1) or type your answer",
		LangChinese:            "如果按钮无响应，请回复选项编号（如 1）或直接输入你的回答",
		LangTraditionalChinese: "若按鈕無回應，請回覆選項編號（如 1）或直接輸入你的回答",
		LangJapanese:           "ボタンが反応しない場合は、番号（例: 1）で返信するか、直接回答を入力してください",
		LangSpanish:            "Si los botones no responden, responda con el número de opción (ej. 1) o escriba su respuesta",
	},
	MsgAskQuestionNoteMulti: {
		LangEnglish:            "Reply with comma-separated option numbers (e.g. 1,3) or type your answer",
		LangChinese:            "请回复逗号分隔的选项编号（如 1,3）或直接输入你的回答",
		LangTraditionalChinese: "請回覆逗號分隔的選項編號（如 1,3）或直接輸入你的回答",
		LangJapanese:           "カンマ区切りの番号（例: 1,3）で返信するか、直接回答を入力してください",
		LangSpanish:            "Responda con los números de opción separados por comas (ej. 1,3) o escriba su respuesta",
	},
	MsgAskQuestionMulti: {
		LangEnglish:            " (multiple selections allowed, separate with commas)",
		LangChinese:            "（可多选，用逗号分隔）",
		LangTraditionalChinese: "（可多選，用逗號分隔）",
		LangJapanese:           "（複数選択可、カンマで区切る）",
		LangSpanish:            " (selección múltiple permitida, separe con comas)",
	},
	MsgAskQuestionPrompt: {
		LangEnglish:            "❓ **%s**\n\n%s\n\nReply with the option number or type your answer.",
		LangChinese:            "❓ **%s**\n\n%s\n\n请回复选项编号或直接输入你的回答。",
		LangTraditionalChinese: "❓ **%s**\n\n%s\n\n請回覆選項編號或直接輸入你的回答。",
		LangJapanese:           "❓ **%s**\n\n%s\n\n番号で返信するか、回答を直接入力してください。",
		LangSpanish:            "❓ **%s**\n\n%s\n\nResponda con el número de opción o escriba su respuesta.",
	},
	MsgAskQuestionAnswered: {
		LangEnglish:            "Answer",
		LangChinese:            "已回答",
		LangTraditionalChinese: "已回答",
		LangJapanese:           "回答済み",
		LangSpanish:            "Respondido",
	},
	MsgCommandsTitle: {
		LangEnglish:            "🔧 **Custom Commands** (%d)\n\n",
		LangChinese:            "🔧 **自定义命令** (%d)\n\n",
		LangTraditionalChinese: "🔧 **自訂命令** (%d)\n\n",
		LangJapanese:           "🔧 **カスタムコマンド** (%d)\n\n",
		LangSpanish:            "🔧 **Comandos personalizados** (%d)\n\n",
	},
	MsgCommandsEmpty: {
		LangEnglish:            "No custom commands configured.\n\nUse `/commands add <name> <prompt>` or add `[[commands]]` in config.toml.",
		LangChinese:            "未配置自定义命令。\n\n使用 `/commands add <名称> <prompt>` 添加，或在 config.toml 中配置 `[[commands]]`。",
		LangTraditionalChinese: "未配置自訂命令。\n\n使用 `/commands add <名稱> <prompt>` 新增，或在 config.toml 中配置 `[[commands]]`。",
		LangJapanese:           "カスタムコマンドが設定されていません。\n\n`/commands add <名前> <プロンプト>` で追加するか、config.toml に `[[commands]]` を追加してください。",
		LangSpanish:            "No hay comandos personalizados configurados.\n\nUse `/commands add <nombre> <prompt>` o agregue `[[commands]]` en config.toml.",
	},
	MsgCommandsHint: {
		LangEnglish:            "Type `/<name> [args]` to use.\n`/commands add <name> <prompt>` to add prompt command\n`/commands addexec <name> <shell>` to add exec command\n`/commands del <name>` to remove",
		LangChinese:            "输入 `/<名称> [参数]` 使用。\n`/commands add <名称> <prompt>` 添加 prompt 命令\n`/commands addexec <名称> <shell命令>` 添加 exec 命令\n`/commands del <名称>` 删除",
		LangTraditionalChinese: "輸入 `/<名稱> [參數]` 使用。\n`/commands add <名稱> <prompt>` 新增 prompt 命令\n`/commands addexec <名稱> <shell命令>` 新增 exec 命令\n`/commands del <名稱>` 刪除",
		LangJapanese:           "`/<名前> [引数]` で使用。\n`/commands add <名前> <プロンプト>` プロンプトコマンド追加\n`/commands addexec <名前> <シェルコマンド>` execコマンド追加\n`/commands del <名前>` 削除",
		LangSpanish:            "Escriba `/<nombre> [args]` para usar.\n`/commands add <nombre> <prompt>` agregar comando prompt\n`/commands addexec <nombre> <shell>` agregar comando exec\n`/commands del <nombre>` eliminar",
	},
	MsgCommandsUsage: {
		LangEnglish:            "Usage:\n`/commands` — list all custom commands\n`/commands add <name> <prompt>` — add prompt command\n`/commands addexec <name> <shell>` — add exec command\n`/commands del <name>` — remove a command",
		LangChinese:            "用法：\n`/commands` — 列出所有自定义命令\n`/commands add <名称> <prompt>` — 添加 prompt 命令\n`/commands addexec <名称> <shell命令>` — 添加 exec 命令\n`/commands del <名称>` — 删除命令",
		LangTraditionalChinese: "用法：\n`/commands` — 列出所有自訂命令\n`/commands add <名稱> <prompt>` — 新增 prompt 命令\n`/commands addexec <名稱> <shell命令>` — 新增 exec 命令\n`/commands del <名稱>` — 刪除命令",
		LangJapanese:           "使い方:\n`/commands` — カスタムコマンド一覧\n`/commands add <名前> <プロンプト>` — プロンプトコマンド追加\n`/commands addexec <名前> <シェルコマンド>` — execコマンド追加\n`/commands del <名前>` — コマンド削除",
		LangSpanish:            "Uso:\n`/commands` — listar comandos personalizados\n`/commands add <nombre> <prompt>` — agregar comando prompt\n`/commands addexec <nombre> <shell>` — agregar comando exec\n`/commands del <nombre>` — eliminar comando",
	},
	MsgCommandsAddUsage: {
		LangEnglish:            "Usage: `/commands add <name> <prompt template>`\n\nExample: `/commands add finduser Search the database for user「{{1}}」`",
		LangChinese:            "用法：`/commands add <名称> <prompt 模板>`\n\n示例：`/commands add finduser 在数据库中查找用户「{{1}}」`",
		LangTraditionalChinese: "用法：`/commands add <名稱> <prompt 模板>`\n\n範例：`/commands add finduser 在資料庫中查找用戶「{{1}}」`",
		LangJapanese:           "使い方: `/commands add <名前> <プロンプトテンプレート>`\n\n例: `/commands add finduser データベースでユーザー「{{1}}」を検索`",
		LangSpanish:            "Uso: `/commands add <nombre> <plantilla prompt>`\n\nEjemplo: `/commands add finduser Buscar en la base de datos al usuario「{{1}}」`",
	},
	MsgCommandsAddExecUsage: {
		LangEnglish:            "Usage: `/commands addexec <name> <shell command>`\n         `/commands addexec --work-dir <dir> <name> <shell command>`\n\nExamples:\n`/commands addexec push git push`\n`/commands addexec status git status {{args}}`",
		LangChinese:            "用法：`/commands addexec <名称> <shell 命令>`\n      `/commands addexec --work-dir <目录> <名称> <shell 命令>`\n\n示例：\n`/commands addexec push git push`\n`/commands addexec status git status {{args}}`",
		LangTraditionalChinese: "用法：`/commands addexec <名稱> <shell 命令>`\n      `/commands addexec --work-dir <目錄> <名稱> <shell 命令>`\n\n範例：\n`/commands addexec push git push`\n`/commands addexec status git status {{args}}`",
		LangJapanese:           "使い方: `/commands addexec <名前> <シェルコマンド>`\n         `/commands addexec --work-dir <ディレクトリ> <名前> <シェルコマンド>`\n\n例:\n`/commands addexec push git push`\n`/commands addexec status git status {{args}}`",
		LangSpanish:            "Uso: `/commands addexec <nombre> <comando shell>`\n      `/commands addexec --work-dir <dir> <nombre> <comando shell>`\n\nEjemplos:\n`/commands addexec push git push`\n`/commands addexec status git status {{args}}`",
	},
	MsgCommandsAdded: {
		LangEnglish:            "✅ Command `/%s` added.\nPrompt: %s",
		LangChinese:            "✅ 命令 `/%s` 已添加。\nPrompt: %s",
		LangTraditionalChinese: "✅ 命令 `/%s` 已新增。\nPrompt: %s",
		LangJapanese:           "✅ コマンド `/%s` を追加しました。\nプロンプト: %s",
		LangSpanish:            "✅ Comando `/%s` agregado.\nPrompt: %s",
	},
	MsgCommandsAddExists: {
		LangEnglish:            "❌ Command `/%s` already exists. Remove it first with `/commands del %s`.",
		LangChinese:            "❌ 命令 `/%s` 已存在。请先使用 `/commands del %s` 删除。",
		LangTraditionalChinese: "❌ 命令 `/%s` 已存在。請先使用 `/commands del %s` 刪除。",
		LangJapanese:           "❌ コマンド `/%s` は既に存在します。`/commands del %s` で削除してから追加してください。",
		LangSpanish:            "❌ El comando `/%s` ya existe. Elimínelo primero con `/commands del %s`.",
	},
	MsgCommandsDelUsage: {
		LangEnglish:            "Usage: `/commands del <name>`",
		LangChinese:            "用法：`/commands del <名称>`",
		LangTraditionalChinese: "用法：`/commands del <名稱>`",
		LangJapanese:           "使い方: `/commands del <名前>`",
		LangSpanish:            "Uso: `/commands del <nombre>`",
	},
	MsgCommandsDeleted: {
		LangEnglish:            "✅ Command `/%s` removed.",
		LangChinese:            "✅ 命令 `/%s` 已删除。",
		LangTraditionalChinese: "✅ 命令 `/%s` 已刪除。",
		LangJapanese:           "✅ コマンド `/%s` を削除しました。",
		LangSpanish:            "✅ Comando `/%s` eliminado.",
	},
	MsgCommandsNotFound: {
		LangEnglish:            "❌ Command `/%s` not found. Use `/commands` to see available commands.",
		LangChinese:            "❌ 命令 `/%s` 未找到。使用 `/commands` 查看可用命令。",
		LangTraditionalChinese: "❌ 命令 `/%s` 未找到。使用 `/commands` 查看可用命令。",
		LangJapanese:           "❌ コマンド `/%s` が見つかりません。`/commands` で一覧を確認してください。",
		LangSpanish:            "❌ Comando `/%s` no encontrado. Use `/commands` para ver los comandos disponibles.",
	},
	MsgCommandsExecAdded: {
		LangEnglish:            "✅ Exec command `/%s` added.\nCommand: %s",
		LangChinese:            "✅ Exec 命令 `/%s` 已添加。\n命令: %s",
		LangTraditionalChinese: "✅ Exec 命令 `/%s` 已新增。\n命令: %s",
		LangJapanese:           "✅ Exec コマンド `/%s` を追加しました。\nコマンド: %s",
		LangSpanish:            "✅ Comando exec `/%s` agregado.\nComando: %s",
	},
	MsgCommandExecTimeout: {
		LangEnglish:            "⏱️ Command `/%s` timed out (60s limit).",
		LangChinese:            "⏱️ 命令 `/%s` 超时（60秒限制）。",
		LangTraditionalChinese: "⏱️ 命令 `/%s` 超時（60秒限制）。",
		LangJapanese:           "⏱️ コマンド `/%s` がタイムアウトしました（60秒制限）。",
		LangSpanish:            "⏱️ Comando `/%s` agotó el tiempo (límite 60s).",
	},
	MsgCommandExecError: {
		LangEnglish:            "❌ Command `/%s` failed:\n%s",
		LangChinese:            "❌ 命令 `/%s` 执行失败：\n%s",
		LangTraditionalChinese: "❌ 命令 `/%s` 執行失敗：\n%s",
		LangJapanese:           "❌ コマンド `/%s` が失敗しました：\n%s",
		LangSpanish:            "❌ Comando `/%s` falló:\n%s",
	},
	MsgCommandExecSuccess: {
		LangEnglish:            "✅ Command executed successfully (no output).",
		LangChinese:            "✅ 命令执行成功（无输出）。",
		LangTraditionalChinese: "✅ 命令執行成功（無輸出）。",
		LangJapanese:           "✅ コマンドが正常に実行されました（出力なし）。",
		LangSpanish:            "✅ Comando ejecutado exitosamente (sin salida).",
	},
	MsgSkillsTitle: {
		LangEnglish:            "📋 Available Skills (%s) — %d skill(s)\n\n",
		LangChinese:            "📋 可用 Skills (%s) — %d 个\n\n",
		LangTraditionalChinese: "📋 可用 Skills (%s) — %d 個\n\n",
		LangJapanese:           "📋 利用可能なスキル (%s) — %d 個\n\n",
		LangSpanish:            "📋 Skills disponibles (%s) — %d skill(s)\n\n",
	},
	MsgSkillsEmpty: {
		LangEnglish:            "No skills found.\nSkills are discovered from agent directories (e.g. .claude/skills/<name>/SKILL.md).",
		LangChinese:            "未发现任何 Skill。\nSkill 从 Agent 目录自动发现（如 .claude/skills/<name>/SKILL.md）。",
		LangTraditionalChinese: "未發現任何 Skill。\nSkill 從 Agent 目錄自動發現（如 .claude/skills/<name>/SKILL.md）。",
		LangJapanese:           "スキルが見つかりません。\nスキルはエージェントのディレクトリから自動検出されます（例: .claude/skills/<name>/SKILL.md）。",
		LangSpanish:            "No se encontraron skills.\nLos skills se descubren de los directorios del agente (ej. .claude/skills/<name>/SKILL.md).",
	},
	MsgSkillsHint: {
		LangEnglish:            "Usage: /<skill-name> [args...] to invoke a skill.",
		LangChinese:            "用法：/<skill名称> [参数...] 来调用 Skill。",
		LangTraditionalChinese: "用法：/<skill名稱> [參數...] 來調用 Skill。",
		LangJapanese:           "使い方：/<スキル名> [引数...] でスキルを実行します。",
		LangSpanish:            "Uso: /<nombre-skill> [args...] para invocar un skill.",
	},

	MsgConfigTitle: {
		LangEnglish:            "⚙️ **Runtime Configuration**\n\n",
		LangChinese:            "⚙️ **运行时配置**\n\n",
		LangTraditionalChinese: "⚙️ **執行階段配置**\n\n",
		LangJapanese:           "⚙️ **ランタイム設定**\n\n",
		LangSpanish:            "⚙️ **Configuración en tiempo de ejecución**\n\n",
	},
	MsgConfigHint: {
		LangEnglish: "Usage:\n" +
			"`/config` — show all\n" +
			"`/config thinking_max_len 200` — update\n" +
			"`/config get thinking_max_len` — view single\n\n" +
			"Set to `0` to disable truncation.",
		LangChinese: "用法：\n" +
			"`/config` — 查看所有配置\n" +
			"`/config thinking_max_len 200` — 修改配置\n" +
			"`/config get thinking_max_len` — 查看单项\n\n" +
			"设为 `0` 表示不截断。",
		LangTraditionalChinese: "用法：\n" +
			"`/config` — 查看所有配置\n" +
			"`/config thinking_max_len 200` — 修改配置\n" +
			"`/config get thinking_max_len` — 查看單項\n\n" +
			"設為 `0` 表示不截斷。",
		LangJapanese: "使い方:\n" +
			"`/config` — 全設定を表示\n" +
			"`/config thinking_max_len 200` — 変更\n" +
			"`/config get thinking_max_len` — 単一確認\n\n" +
			"`0` = 切り捨てなし",
		LangSpanish: "Uso:\n" +
			"`/config` — ver todo\n" +
			"`/config thinking_max_len 200` — actualizar\n" +
			"`/config get thinking_max_len` — ver uno\n\n" +
			"Establecer `0` para no truncar.",
	},
	MsgConfigGetUsage: {
		LangEnglish:            "Usage: `/config get thinking_max_len`",
		LangChinese:            "用法：`/config get thinking_max_len`",
		LangTraditionalChinese: "用法：`/config get thinking_max_len`",
		LangJapanese:           "使い方: `/config get thinking_max_len`",
		LangSpanish:            "Uso: `/config get thinking_max_len`",
	},
	MsgConfigSetUsage: {
		LangEnglish:            "Usage: `/config set thinking_max_len 200`",
		LangChinese:            "用法：`/config set thinking_max_len 200`",
		LangTraditionalChinese: "用法：`/config set thinking_max_len 200`",
		LangJapanese:           "使い方: `/config set thinking_max_len 200`",
		LangSpanish:            "Uso: `/config set thinking_max_len 200`",
	},
	MsgConfigUpdated: {
		LangEnglish:            "✅ `%s` → `%s`",
		LangChinese:            "✅ `%s` → `%s`",
		LangTraditionalChinese: "✅ `%s` → `%s`",
		LangJapanese:           "✅ `%s` → `%s`",
		LangSpanish:            "✅ `%s` → `%s`",
	},
	MsgConfigKeyNotFound: {
		LangEnglish:            "❌ Unknown config key `%s`. Use `/config` to see available keys.",
		LangChinese:            "❌ 未知配置项 `%s`。使用 `/config` 查看可用配置。",
		LangTraditionalChinese: "❌ 未知配置項 `%s`。使用 `/config` 查看可用配置。",
		LangJapanese:           "❌ 不明な設定キー `%s`。`/config` で一覧を確認してください。",
		LangSpanish:            "❌ Clave de configuración desconocida `%s`. Use `/config` para ver las disponibles.",
	},
	MsgConfigReloaded: {
		LangEnglish:            "✅ Config reloaded\n\nDisplay updated: %v\nProviders synced: %d\nCommands synced: %d",
		LangChinese:            "✅ 配置已重新加载\n\n显示设置已更新：%v\nProvider 已同步：%d 个\n自定义命令已同步：%d 个",
		LangTraditionalChinese: "✅ 配置已重新載入\n\n顯示設定已更新：%v\nProvider 已同步：%d 個\n自訂命令已同步：%d 個",
		LangJapanese:           "✅ 設定をリロードしました\n\n表示設定更新: %v\nプロバイダ同期: %d 件\nコマンド同期: %d 件",
		LangSpanish:            "✅ Configuración recargada\n\nPantalla actualizada: %v\nProveedores sincronizados: %d\nComandos sincronizados: %d",
	},
	MsgDoctorRunning: {
		LangEnglish:            "🏥 Running diagnostics...",
		LangChinese:            "🏥 正在运行系统诊断...",
		LangTraditionalChinese: "🏥 正在執行系統診斷...",
		LangJapanese:           "🏥 診断を実行中...",
		LangSpanish:            "🏥 Ejecutando diagnósticos...",
	},
	MsgDoctorTitle: {
		LangEnglish:            "🏥 **System Diagnostic Report**\n\n",
		LangChinese:            "🏥 **系统诊断报告**\n\n",
		LangTraditionalChinese: "🏥 **系統診斷報告**\n\n",
		LangJapanese:           "🏥 **システム診断レポート**\n\n",
		LangSpanish:            "🏥 **Informe de diagnóstico del sistema**\n\n",
	},
	MsgDoctorSummary: {
		LangEnglish:            "\n✅ %d passed  ⚠️ %d warnings  ❌ %d failed",
		LangChinese:            "\n✅ %d 项通过  ⚠️ %d 项警告  ❌ %d 项失败",
		LangTraditionalChinese: "\n✅ %d 項通過  ⚠️ %d 項警告  ❌ %d 項失敗",
		LangJapanese:           "\n✅ %d 合格  ⚠️ %d 警告  ❌ %d 失敗",
		LangSpanish:            "\n✅ %d aprobados  ⚠️ %d advertencias  ❌ %d fallidos",
	},
	MsgRestarting: {
		LangEnglish:            "🔄 Restarting lark-agent-bot...",
		LangChinese:            "🔄 正在重启 lark-agent-bot...",
		LangTraditionalChinese: "🔄 正在重啟 lark-agent-bot...",
		LangJapanese:           "🔄 lark-agent-bot を再起動中...",
		LangSpanish:            "🔄 Reiniciando lark-agent-bot...",
	},
	MsgRestartSuccess: {
		LangEnglish:            "✅ lark-agent-bot restarted successfully.",
		LangChinese:            "✅ lark-agent-bot 重启成功。",
		LangTraditionalChinese: "✅ lark-agent-bot 重啟成功。",
		LangJapanese:           "✅ lark-agent-bot の再起動が完了しました。",
		LangSpanish:            "✅ lark-agent-bot se reinició correctamente.",
	},
	MsgUpgradeChecking: {
		LangEnglish:            "🔍 Checking for updates...",
		LangChinese:            "🔍 正在检查更新...",
		LangTraditionalChinese: "🔍 正在檢查更新...",
		LangJapanese:           "🔍 アップデートを確認中...",
		LangSpanish:            "🔍 Buscando actualizaciones...",
	},
	MsgUpgradeUpToDate: {
		LangEnglish:            "✅ Already up to date (%s)",
		LangChinese:            "✅ 已是最新版本 (%s)",
		LangTraditionalChinese: "✅ 已是最新版本 (%s)",
		LangJapanese:           "✅ 最新バージョンです (%s)",
		LangSpanish:            "✅ Ya está actualizado (%s)",
	},
	MsgUpgradeAvailable: {
		LangEnglish: "🆕 New version available!\n\n\n" +
			"Current: **%s**\n" +
			"Latest:  **%s**\n\n\n" +
			"%s\n\n\n" +
			"Run `/upgrade confirm` to install.",
		LangChinese: "🆕 发现新版本！\n\n\n" +
			"当前版本：**%s**\n" +
			"最新版本：**%s**\n\n\n" +
			"%s\n\n\n" +
			"执行 `/upgrade confirm` 进行更新。",
		LangTraditionalChinese: "🆕 發現新版本！\n\n\n" +
			"當前版本：**%s**\n" +
			"最新版本：**%s**\n\n\n" +
			"%s\n\n\n" +
			"執行 `/upgrade confirm` 進行更新。",
		LangJapanese: "🆕 新しいバージョンがあります！\n\n\n" +
			"現在: **%s**\n" +
			"最新: **%s**\n\n\n" +
			"%s\n\n" +
			"`/upgrade confirm` でインストール。",
		LangSpanish: "🆕 ¡Nueva versión disponible!\n\n\n" +
			"Actual: **%s**\n" +
			"Última: **%s**\n\n\n" +
			"%s\n\n\n" +
			"Ejecute `/upgrade confirm` para instalar.",
	},
	MsgUpgradeDownloading: {
		LangEnglish:            "⬇️ Downloading %s ...",
		LangChinese:            "⬇️ 正在下载 %s ...",
		LangTraditionalChinese: "⬇️ 正在下載 %s ...",
		LangJapanese:           "⬇️ ダウンロード中 %s ...",
		LangSpanish:            "⬇️ Descargando %s ...",
	},
	MsgUpgradeSuccess: {
		LangEnglish:            "✅ Updated to **%s** successfully!",
		LangChinese:            "✅ 已成功更新到 **%s**！",
		LangTraditionalChinese: "✅ 已成功更新到 **%s**！",
		LangJapanese:           "✅ **%s** に更新しました！",
		LangSpanish:            "✅ ¡Actualizado a **%s** con éxito!",
	},
	MsgUpgradeAlreadyInstalled: {
		LangEnglish:            "✅ The program on disk is already **%s** (updated by another bot sharing it); a restart loads it.",
		LangChinese:            "✅ 磁盘上的程序已经是 **%s**（共用该程序的其他机器人已完成更新），重启后加载新版本。",
		LangTraditionalChinese: "✅ 磁碟上的程式已經是 **%s**（共用該程式的其他機器人已完成更新），重啟後載入新版本。",
		LangJapanese:           "✅ ディスク上のプログラムは既に **%s** です（共有している別のボットが更新済み）。再起動すると読み込まれます。",
		LangSpanish:            "✅ El programa en disco ya es **%s** (lo actualizó otro bot que lo comparte); se carga al reiniciar.",
	},
	MsgPeerVersionMismatch: {
		LangEnglish:            "⚠️ Other bots on this machine run a version other than %s: %s. Relays and @ handoffs between the bots can fail until they all run the same version; upgrade or restart them too.",
		LangChinese:            "⚠️ 本机其他机器人运行的版本不是 %s：%s。版本一致之前，机器人之间的转派（relay、@ 交接）可能出错，请一并升级或重启它们。",
		LangTraditionalChinese: "⚠️ 本機其他機器人執行的版本不是 %s：%s。版本一致之前，機器人之間的轉派（relay、@ 交接）可能出錯，請一併升級或重新啟動它們。",
		LangJapanese:           "⚠️ このマシンの他のボットは %s 以外のバージョンで動作しています：%s。バージョンが揃うまで、ボット間のリレーや @ での引き継ぎが失敗することがあります。あわせてアップグレードまたは再起動してください。",
		LangSpanish:            "⚠️ Otros bots de esta máquina ejecutan una versión distinta de %s: %s. Los relevos y traspasos con @ entre ellos pueden fallar hasta que todos usen la misma versión; actualízalos o reinícialos también.",
	},
	MsgUpgradeRestartWaiting: {
		LangEnglish:            "⏳ %d task(s) still in progress. The restart waits for them to finish (up to %d min). Send /restart to restart now.",
		LangChinese:            "⏳ 还有 %d 个任务在处理，等它们做完再重启（最多等 %d 分钟）。要马上重启，发 /restart。",
		LangTraditionalChinese: "⏳ 還有 %d 個任務在處理，等它們做完再重啟（最多等 %d 分鐘）。要馬上重啟，傳送 /restart。",
		LangJapanese:           "⏳ 処理中のタスクが %d 件あります。完了を待ってから再起動します（最大 %d 分）。すぐに再起動するには /restart を送信してください。",
		LangSpanish:            "⏳ Hay %d tarea(s) en curso. El reinicio espera a que terminen (hasta %d min). Envía /restart para reiniciar ahora.",
	},
	MsgUpgradeConfirmButton: {
		LangEnglish:            "Upgrade now",
		LangChinese:            "立即升级",
		LangTraditionalChinese: "立即升級",
		LangJapanese:           "今すぐ更新",
		LangSpanish:            "Actualizar ahora",
	},
	MsgUpgradeDevBuild: {
		LangEnglish:            "⚠️ Running a dev build — version check is not available. Please build from source or install a release version.",
		LangChinese:            "⚠️ 当前为开发版本，无法检查更新。请从源码构建或安装正式发布版本。",
		LangTraditionalChinese: "⚠️ 當前為開發版本，無法檢查更新。請從源碼構建或安裝正式發佈版本。",
		LangJapanese:           "⚠️ 開発ビルドのため、バージョン確認ができません。ソースからビルドするか、リリース版をインストールしてください。",
		LangSpanish:            "⚠️ Compilación de desarrollo — la verificación de versión no está disponible. Compile desde el código fuente o instale una versión publicada.",
	},
	MsgWebNotSupported: {
		LangEnglish:            "⚠️ Web admin is not in this build. Use a release binary or the npm package, or build from source with `make build`.",
		LangChinese:            "⚠️ 当前程序没有包含 Web 管理后台。请使用 Release 里的程序或 npm 包，或者从源码用 `make build` 构建。",
		LangTraditionalChinese: "⚠️ 目前程式未包含 Web 管理後台。請使用 Release 裡的程式或 npm 套件，或從原始碼用 `make build` 建置。",
		LangJapanese:           "⚠️ このビルドにはWeb管理画面が含まれていません。リリース版のバイナリかnpmパッケージを使うか、ソースから `make build` でビルドしてください。",
		LangSpanish:            "⚠️ La administración web no está incluida en esta compilación. Use un binario de las releases o el paquete npm, o compile desde el código con `make build`.",
	},
	MsgWebNotEnabled: {
		LangEnglish:            "ℹ️ Web admin is not enabled.\n\nUse `/web setup` to configure and enable it.",
		LangChinese:            "ℹ️ Web 管理后台未启用。\n\n使用 `/web setup` 配置并启用。",
		LangTraditionalChinese: "ℹ️ Web 管理後台未啟用。\n\n使用 `/web setup` 設定並啟用。",
		LangJapanese:           "ℹ️ Web管理画面は有効になっていません。\n\n`/web setup` で設定して有効にしてください。",
		LangSpanish:            "ℹ️ La administración web no está habilitada.\n\nUsa `/web setup` para configurarla.",
	},
	MsgWebSetupSuccess: {
		LangEnglish: "✅ Web admin configured!\n\n" +
			"🌐 URL: %s\n🔑 Token: `%s`\n\n" +
			"Open the URL in your browser and use the token to log in.",
		LangChinese: "✅ Web 管理后台配置完成！\n\n" +
			"🌐 地址：%s\n🔑 令牌：`%s`\n\n" +
			"在浏览器打开地址，使用令牌登录。",
		LangTraditionalChinese: "✅ Web 管理後台設定完成！\n\n" +
			"🌐 網址：%s\n🔑 權杖：`%s`\n\n" +
			"在瀏覽器開啟網址，使用權杖登入。",
		LangJapanese: "✅ Web管理画面の設定が完了しました！\n\n" +
			"🌐 URL: %s\n🔑 トークン: `%s`\n\n" +
			"ブラウザでURLを開き、トークンでログインしてください。",
		LangSpanish: "✅ Administración web configurada!\n\n" +
			"🌐 URL: %s\n🔑 Token: `%s`\n\n" +
			"Abre la URL en tu navegador y usa el token para iniciar sesión.",
	},
	MsgWebNeedRestart: {
		LangEnglish:            "🔄 Restart the service with `/restart` to activate the web admin.",
		LangChinese:            "🔄 请使用 `/restart` 重启服务以激活 Web 管理后台。",
		LangTraditionalChinese: "🔄 請使用 `/restart` 重新啟動服務以啟動 Web 管理後台。",
		LangJapanese:           "🔄 `/restart` でサービスを再起動して、Web管理画面を有効にしてください。",
		LangSpanish:            "🔄 Reinicia el servicio con `/restart` para activar la administración web.",
	},
	MsgWebStatus: {
		LangEnglish:            "🌐 **Web Admin**\n\nURL: %s",
		LangChinese:            "🌐 **Web 管理后台**\n\n地址：%s",
		LangTraditionalChinese: "🌐 **Web 管理後台**\n\n網址：%s",
		LangJapanese:           "🌐 **Web管理画面**\n\nURL: %s",
		LangSpanish:            "🌐 **Administración Web**\n\nURL: %s",
	},
	MsgAliasEmpty: {
		LangEnglish:            "No aliases configured. Use `/alias add <trigger> <command>` to create one.",
		LangChinese:            "暂无别名配置。使用 `/alias add <触发词> <命令>` 创建别名。",
		LangTraditionalChinese: "尚無別名配置。使用 `/alias add <觸發詞> <命令>` 建立別名。",
		LangJapanese:           "エイリアスは設定されていません。`/alias add <トリガー> <コマンド>` で作成してください。",
		LangSpanish:            "No hay alias configurados. Use `/alias add <trigger> <comando>` para crear uno.",
	},
	MsgAliasListHeader: {
		LangEnglish:            "📎 Aliases (%d)",
		LangChinese:            "📎 命令别名 (%d)",
		LangTraditionalChinese: "📎 命令別名 (%d)",
		LangJapanese:           "📎 エイリアス (%d)",
		LangSpanish:            "📎 Alias (%d)",
	},
	MsgAliasAdded: {
		LangEnglish:            "✅ Alias added: %s → %s",
		LangChinese:            "✅ 别名已添加：%s → %s",
		LangTraditionalChinese: "✅ 別名已新增：%s → %s",
		LangJapanese:           "✅ エイリアス追加：%s → %s",
		LangSpanish:            "✅ Alias añadido: %s → %s",
	},
	MsgAliasDeleted: {
		LangEnglish:            "✅ Alias removed: %s",
		LangChinese:            "✅ 别名已删除：%s",
		LangTraditionalChinese: "✅ 別名已刪除：%s",
		LangJapanese:           "✅ エイリアス削除：%s",
		LangSpanish:            "✅ Alias eliminado: %s",
	},
	MsgAliasNotFound: {
		LangEnglish:            "❌ Alias `%s` not found.",
		LangChinese:            "❌ 别名 `%s` 不存在。",
		LangTraditionalChinese: "❌ 別名 `%s` 不存在。",
		LangJapanese:           "❌ エイリアス `%s` が見つかりません。",
		LangSpanish:            "❌ Alias `%s` no encontrado.",
	},
	MsgAliasUsage: {
		LangEnglish:            "Usage:\n  `/alias` — list all aliases\n  `/alias add <trigger> <command>` — add alias\n  `/alias del <trigger>` — remove alias\n\nExample: `/alias add 帮助 /help`",
		LangChinese:            "用法：\n  `/alias` — 列出所有别名\n  `/alias add <触发词> <命令>` — 添加别名\n  `/alias del <触发词>` — 删除别名\n\n示例：`/alias add 帮助 /help`",
		LangTraditionalChinese: "用法：\n  `/alias` — 列出所有別名\n  `/alias add <觸發詞> <命令>` — 新增別名\n  `/alias del <觸發詞>` — 刪除別名\n\n範例：`/alias add 幫助 /help`",
		LangJapanese:           "使い方：\n  `/alias` — エイリアス一覧\n  `/alias add <トリガー> <コマンド>` — 追加\n  `/alias del <トリガー>` — 削除\n\n例: `/alias add ヘルプ /help`",
		LangSpanish:            "Uso:\n  `/alias` — listar aliases\n  `/alias add <trigger> <comando>` — añadir alias\n  `/alias del <trigger>` — eliminar alias\n\nEjemplo: `/alias add ayuda /help`",
	},
	MsgNewSessionCreated: {
		LangEnglish:            "✅ New session created",
		LangChinese:            "✅ 新会话已创建",
		LangTraditionalChinese: "✅ 新會話已建立",
		LangJapanese:           "✅ 新しいセッションを作成しました",
		LangSpanish:            "✅ Nueva sesión creada",
	},
	MsgNewSessionCreatedName: {
		LangEnglish:            "✅ New session created: **%s**",
		LangChinese:            "✅ 新会话已创建：**%s**",
		LangTraditionalChinese: "✅ 新會話已建立：**%s**",
		LangJapanese:           "✅ 新しいセッションを作成しました：**%s**",
		LangSpanish:            "✅ Nueva sesión creada: **%s**",
	},
	MsgSessionAutoResetIdle: {
		LangEnglish:            "⏰ Session auto-reset after %d minute(s) of inactivity.",
		LangChinese:            "⏰ 因空闲超过 %d 分钟，已自动切换到新会话。",
		LangTraditionalChinese: "⏰ 因閒置超過 %d 分鐘，已自動切換到新會話。",
		LangJapanese:           "⏰ %d 分以上操作がなかったため、新しいセッションに自動切り替えました。",
		LangSpanish:            "⏰ La sesión se reinició automáticamente tras %d minuto(s) de inactividad.",
	},
	MsgSessionClosingGraceful: {
		LangEnglish:            "⏳ Wrapping up your previous session (usually a few seconds, up to 2 minutes). Your new session will start automatically.",
		LangChinese:            "⏳ 正在结束上一个会话（通常几秒钟，最多2分钟）。新会话将自动启动。",
		LangTraditionalChinese: "⏳ 正在結束上一個會話（通常幾秒鐘，最多2分鐘）。新會話將自動啟動。",
		LangJapanese:           "⏳ 前のセッションを終了中です（通常は数秒、最大2分）。新しいセッションは自動的に開始されます。",
		LangSpanish:            "⏳ Cerrando la sesión anterior (normalmente unos segundos, hasta 2 minutos). La nueva sesión se iniciará automáticamente.",
	},
	MsgDeleteUsage: {
		LangEnglish:            "Usage: `/delete <number>` or `/delete 1,2,3` or `/delete 3-7` or `/delete 1,3-5,8`.\nUse `/list` to see session numbers.",
		LangChinese:            "用法：`/delete <序号>`，或 `/delete 1,2,3`，或 `/delete 3-7`，或 `/delete 1,3-5,8`。\n使用 `/list` 查看会话序号。",
		LangTraditionalChinese: "用法：`/delete <序號>`，或 `/delete 1,2,3`，或 `/delete 3-7`，或 `/delete 1,3-5,8`。\n使用 `/list` 查看會話序號。",
		LangJapanese:           "使い方：`/delete <番号>`、または `/delete 1,2,3`、または `/delete 3-7`、または `/delete 1,3-5,8`。\n`/list` で番号を確認できます。",
		LangSpanish:            "Uso: `/delete <número>` o `/delete 1,2,3` o `/delete 3-7` o `/delete 1,3-5,8`.\nUse `/list` para ver los números.",
	},
	MsgDeleteSuccess: {
		LangEnglish:            "🗑️ Session deleted: %s",
		LangChinese:            "🗑️ 会话已删除：%s",
		LangTraditionalChinese: "🗑️ 會話已刪除：%s",
		LangJapanese:           "🗑️ セッション削除：%s",
		LangSpanish:            "🗑️ Sesión eliminada: %s",
	},
	MsgSwitchSuccess: {
		LangEnglish:            "✅ Switched to: %s (%s, %d msgs)",
		LangChinese:            "✅ 已切换到：%s（%s，%d 条消息）",
		LangTraditionalChinese: "✅ 已切換到：%s（%s，%d 則訊息）",
		LangJapanese:           "✅ 切り替え：%s（%s、%d件）",
		LangSpanish:            "✅ Cambiado a: %s (%s, %d mensajes)",
	},
	MsgSwitchNoMatch: {
		LangEnglish:            "❌ No session matching %q",
		LangChinese:            "❌ 没有找到匹配 %q 的会话",
		LangTraditionalChinese: "❌ 沒有找到匹配 %q 的會話",
		LangJapanese:           "❌ %q に一致するセッションが見つかりません",
		LangSpanish:            "❌ No hay sesión que coincida con %q",
	},
	MsgSwitchNoSession: {
		LangEnglish:            "❌ No session #%d",
		LangChinese:            "❌ 没有第 %d 个会话",
		LangTraditionalChinese: "❌ 沒有第 %d 個會話",
		LangJapanese:           "❌ セッション #%d が見つかりません",
		LangSpanish:            "❌ No hay sesión #%d",
	},
	MsgCommandTimeout: {
		LangEnglish:            "⏰ Command timed out (60s): `%s`",
		LangChinese:            "⏰ 命令超时 (60秒): `%s`",
		LangTraditionalChinese: "⏰ 命令逾時 (60秒): `%s`",
		LangJapanese:           "⏰ コマンドがタイムアウトしました (60秒): `%s`",
		LangSpanish:            "⏰ Comando agotado (60s): `%s`",
	},
	MsgDeleteActiveDenied: {
		LangEnglish:            "❌ Cannot delete the currently active session. Switch to another session first.",
		LangChinese:            "❌ 不能删除当前活跃会话，请先切换到其他会话。",
		LangTraditionalChinese: "❌ 不能刪除當前活躍會話，請先切換到其他會話。",
		LangJapanese:           "❌ 現在アクティブなセッションは削除できません。先に別のセッションに切り替えてください。",
		LangSpanish:            "❌ No se puede eliminar la sesión activa. Cambie a otra sesión primero.",
	},
	MsgDeleteNotSupported: {
		LangEnglish:            "❌ This agent does not support session deletion.",
		LangChinese:            "❌ 当前 Agent 不支持删除会话。",
		LangTraditionalChinese: "❌ 當前 Agent 不支持刪除會話。",
		LangJapanese:           "❌ このエージェントはセッション削除をサポートしていません。",
		LangSpanish:            "❌ Este agente no admite la eliminación de sesiones.",
	},
	MsgDeleteModeTitle: {
		LangEnglish:            "Delete Sessions",
		LangChinese:            "删除会话",
		LangTraditionalChinese: "刪除會話",
		LangJapanese:           "セッション削除",
		LangSpanish:            "Eliminar sesiones",
	},
	MsgDeleteModeSelect: {
		LangEnglish:            "Select",
		LangChinese:            "选择",
		LangTraditionalChinese: "選擇",
		LangJapanese:           "選択",
		LangSpanish:            "Seleccionar",
	},
	MsgDeleteModeSelected: {
		LangEnglish:            "Selected",
		LangChinese:            "已选",
		LangTraditionalChinese: "已選",
		LangJapanese:           "選択済み",
		LangSpanish:            "Seleccionado",
	},
	MsgDeleteModeSelectedCount: {
		LangEnglish:            "%d selected",
		LangChinese:            "已选 %d 项",
		LangTraditionalChinese: "已選 %d 項",
		LangJapanese:           "%d 件を選択中",
		LangSpanish:            "%d seleccionadas",
	},
	MsgDeleteModeDeleteSelected: {
		LangEnglish:            "Delete Selected",
		LangChinese:            "删除已选",
		LangTraditionalChinese: "刪除已選",
		LangJapanese:           "選択項目を削除",
		LangSpanish:            "Eliminar seleccionadas",
	},
	MsgDeleteModeCancel: {
		LangEnglish:            "Cancel",
		LangChinese:            "取消",
		LangTraditionalChinese: "取消",
		LangJapanese:           "キャンセル",
		LangSpanish:            "Cancelar",
	},
	MsgDeleteModeConfirmTitle: {
		LangEnglish:            "Confirm Delete",
		LangChinese:            "确认删除",
		LangTraditionalChinese: "確認刪除",
		LangJapanese:           "削除確認",
		LangSpanish:            "Confirmar eliminación",
	},
	MsgDeleteModeConfirmButton: {
		LangEnglish:            "Confirm Delete",
		LangChinese:            "确认删除",
		LangTraditionalChinese: "確認刪除",
		LangJapanese:           "削除を確認",
		LangSpanish:            "Confirmar eliminación",
	},
	MsgDeleteModeBackButton: {
		LangEnglish:            "Back",
		LangChinese:            "返回继续选择",
		LangTraditionalChinese: "返回繼續選擇",
		LangJapanese:           "選択に戻る",
		LangSpanish:            "Volver",
	},
	MsgDeleteModeEmptySelection: {
		LangEnglish:            "Select at least one session.",
		LangChinese:            "请至少选择一个会话。",
		LangTraditionalChinese: "請至少選擇一個會話。",
		LangJapanese:           "少なくとも 1 つのセッションを選択してください。",
		LangSpanish:            "Seleccione al menos una sesión.",
	},
	MsgDeleteModeResultTitle: {
		LangEnglish:            "Delete Result",
		LangChinese:            "删除结果",
		LangTraditionalChinese: "刪除結果",
		LangJapanese:           "削除結果",
		LangSpanish:            "Resultado de eliminación",
	},
	MsgDeleteModeDeletingTitle: {
		LangEnglish:            "Deleting Sessions...",
		LangChinese:            "正在删除会话...",
		LangTraditionalChinese: "正在刪除會話...",
		LangJapanese:           "セッションを削除中...",
		LangSpanish:            "Eliminando sesiones...",
	},
	MsgDeleteModeDeletingBody: {
		LangEnglish:            "Deleting %d session(s), please wait...",
		LangChinese:            "正在删除 %d 个会话，请稍候...",
		LangTraditionalChinese: "正在刪除 %d 個會話，請稍候...",
		LangJapanese:           "%d 件のセッションを削除中、お待ちください...",
		LangSpanish:            "Eliminando %d sesión(es), por favor espere...",
	},
	MsgDeleteModeMissingSession: {
		LangEnglish:            "❌ Missing selected session: %s",
		LangChinese:            "❌ 已选会话不存在：%s",
		LangTraditionalChinese: "❌ 已選會話不存在：%s",
		LangJapanese:           "❌ 選択したセッションが見つかりません: %s",
		LangSpanish:            "❌ Falta la sesión seleccionada: %s",
	},
	MsgBannedWordBlocked: {
		LangEnglish:            "⚠️ Your message was blocked because it contains a prohibited word.",
		LangChinese:            "⚠️ 消息已被拦截，包含违禁词。",
		LangTraditionalChinese: "⚠️ 訊息已被攔截，包含違禁詞。",
		LangJapanese:           "⚠️ 禁止ワードが含まれているため、メッセージがブロックされました。",
		LangSpanish:            "⚠️ Su mensaje fue bloqueado porque contiene una palabra prohibida.",
	},
	MsgCommandDisabled: {
		LangEnglish:            "🚫 Command `%s` is disabled for this project.",
		LangChinese:            "🚫 命令 `%s` 在当前项目中已被禁用。",
		LangTraditionalChinese: "🚫 命令 `%s` 在當前專案中已被停用。",
		LangJapanese:           "🚫 コマンド `%s` はこのプロジェクトで無効化されています。",
		LangSpanish:            "🚫 El comando `%s` está deshabilitado para este proyecto.",
	},
	MsgAdminRequired: {
		LangEnglish:            "🔒 Command `%s` requires admin privilege. Set `admin_from` in config to authorize users.",
		LangChinese:            "🔒 命令 `%s` 需要管理员权限。请在配置中设置 `admin_from` 来授权用户。",
		LangTraditionalChinese: "🔒 命令 `%s` 需要管理員權限。請在配置中設定 `admin_from` 來授權使用者。",
		LangJapanese:           "🔒 コマンド `%s` には管理者権限が必要です。設定で `admin_from` を設定してユーザーを承認してください。",
		LangSpanish:            "🔒 El comando `%s` requiere privilegios de administrador. Configure `admin_from` en la configuración.",
	},
	MsgRateLimited: {
		LangEnglish:            "⏳ You are sending messages too fast. Please wait a moment.",
		LangChinese:            "⏳ 消息发送过快，请稍后再试。",
		LangTraditionalChinese: "⏳ 訊息發送過快，請稍後再試。",
		LangJapanese:           "⏳ メッセージの送信が速すぎます。しばらくお待ちください。",
		LangSpanish:            "⏳ Estás enviando mensajes demasiado rápido. Espera un momento.",
	},
	MsgPsSent: {
		LangEnglish:            "✅ P.S. delivered.",
		LangChinese:            "✅ P.S. 已送达。",
		LangTraditionalChinese: "✅ P.S. 已送達。",
		LangJapanese:           "✅ P.S. を送信しました。",
		LangSpanish:            "✅ P.S. entregado.",
	},
	MsgPsSendFailed: {
		LangEnglish:            "❌ Failed to deliver P.S.",
		LangChinese:            "❌ P.S. 发送失败。",
		LangTraditionalChinese: "❌ P.S. 傳送失敗。",
		LangJapanese:           "❌ P.S. の送信に失敗しました。",
		LangSpanish:            "❌ Error al entregar el P.S.",
	},
	MsgPsEmpty: {
		LangEnglish:            "Usage: `/ps <message>`",
		LangChinese:            "用法：`/ps <消息>`",
		LangTraditionalChinese: "用法：`/ps <訊息>`",
		LangJapanese:           "使い方：`/ps <メッセージ>`",
		LangSpanish:            "Uso: `/ps <mensaje>`",
	},
	MsgPsNoSession: {
		LangEnglish:            "No task is currently running.",
		LangChinese:            "当前没有正在执行的任务。",
		LangTraditionalChinese: "目前沒有正在執行的任務。",
		LangJapanese:           "現在実行中のタスクはありません。",
		LangSpanish:            "No hay ninguna tarea en ejecución.",
	},
	MsgWhoamiTitle: {
		LangEnglish:            "🪪 **Your Identity**",
		LangChinese:            "🪪 **你的身份信息**",
		LangTraditionalChinese: "🪪 **你的身分資訊**",
		LangJapanese:           "🪪 **あなたの身元情報**",
		LangSpanish:            "🪪 **Tu identidad**",
	},
	MsgWhoamiCardTitle: {
		LangEnglish:            "Your Identity",
		LangChinese:            "你的身份信息",
		LangTraditionalChinese: "你的身分資訊",
		LangJapanese:           "あなたの身元情報",
		LangSpanish:            "Tu identidad",
	},
	MsgWhoamiName: {
		LangEnglish:            "Name",
		LangChinese:            "名称",
		LangTraditionalChinese: "名稱",
		LangJapanese:           "名前",
		LangSpanish:            "Nombre",
	},
	MsgWhoamiPlatform: {
		LangEnglish:            "Platform",
		LangChinese:            "平台",
		LangTraditionalChinese: "平台",
		LangJapanese:           "プラットフォーム",
		LangSpanish:            "Plataforma",
	},
	MsgWhoamiUsage: {
		LangEnglish:            "💡 Use the `User ID` above for `allow_from` and `admin_from` in your `config.toml`.",
		LangChinese:            "💡 可将上方 `User ID` 填入 `config.toml` 的 `allow_from` 或 `admin_from` 中。",
		LangTraditionalChinese: "💡 可將上方 `User ID` 填入 `config.toml` 的 `allow_from` 或 `admin_from` 中。",
		LangJapanese:           "💡 上記の `User ID` を `config.toml` の `allow_from` や `admin_from` に設定してください。",
		LangSpanish:            "💡 Usa el `User ID` de arriba para `allow_from` y `admin_from` en tu `config.toml`.",
	},
	MsgRelayNoBinding: {
		LangEnglish: "No relay binding in this chat.\nUse `/bind <project>` to bind another bot.\nThe <project> is the project name from your config.toml.",
		LangChinese: "当前群聊没有中继绑定。\n使用 `/bind <项目名>` 绑定另一个机器人。\n<项目名> 是 config.toml 中 [[projects]] 的 name 字段。",
	},
	MsgRelayBound: {
		LangEnglish: "Current relay binding: %s",
		LangChinese: "当前中继绑定: %s",
	},
	MsgRelayUsage: {
		LangEnglish: "Usage:\n  /bind <project>  — bind with another bot in this group\n  /bind remove     — remove binding\n  /bind            — show current binding\n\n<project> is the project name from config.toml [[projects]].",
		LangChinese: "用法:\n  /bind <项目名>  — 绑定群聊中的另一个机器人\n  /bind remove    — 解除绑定\n  /bind           — 查看当前绑定\n\n<项目名> 是 config.toml 中 [[projects]] 的 name 字段。",
	},
	MsgRelayNotAvailable: {
		LangEnglish: "Relay is not available. Make sure you have multiple projects configured.",
		LangChinese: "中继功能不可用。请确保配置了多个项目。",
	},
	MsgRelayUnbound: {
		LangEnglish: "Relay binding removed.",
		LangChinese: "中继绑定已解除。",
	},
	MsgRelayBindSelf: {
		LangEnglish: "Cannot bind to yourself. Specify a different project.",
		LangChinese: "不能绑定自己，请指定另一个项目。",
	},
	MsgRelayNotFound: {
		LangEnglish: "Project %q not found. Available projects: %s",
		LangChinese: "项目 %q 不存在。可用的项目: %s",
	},
	MsgRelayNoTarget: {
		LangEnglish: "Project %q not found. No other project is configured here or running in another lark-agent-bot process.",
		LangChinese: "项目 %q 不存在。本配置里没有其他项目，也没有其他 lark-agent-bot 进程在运行其他项目。",
	},
	MsgRelayBindRemoved: {
		LangEnglish:            "✅ Removed %s from binding",
		LangChinese:            "✅ 已从绑定中移除 %s",
		LangTraditionalChinese: "✅ 已從綁定中移除 %s",
		LangJapanese:           "✅ %s をバインドから削除しました",
		LangSpanish:            "✅ Eliminado %s del enlace",
	},
	MsgRelayBindNotFound: {
		LangEnglish:            "❌ %s is not bound or binding does not exist",
		LangChinese:            "❌ %s 未绑定或绑定不存在",
		LangTraditionalChinese: "❌ %s 未綁定或綁定不存在",
		LangJapanese:           "❌ %s はバインドされていないか、バインドが存在しません",
		LangSpanish:            "❌ %s no está vinculado o el enlace no existe",
	},
	MsgRelayBindSuccess: {
		LangEnglish:            "✅ Bind successful! Current group bound: %s\n\nYou can now ask this bot to communicate with %s.\nExample: \"Ask %s about ...\"",
		LangChinese:            "✅ 绑定成功！当前群组已绑定: %s\n\n你现在可以让本机器人去询问 %s。\n示例：\"帮我问一下 %s ...\"",
		LangTraditionalChinese: "✅ 綁定成功！當前群組已綁定: %s\n\n你現在可以讓本機器人去詢問 %s。\n示例：\"幫我問一下 %s ...\"",
		LangJapanese:           "✅ バインド成功！現在のグループ: %s\n\nこのボットに %s への問い合わせを依頼できます。\n例：「%s に...を聞いて」",
		LangSpanish:            "✅ ¡Enlace exitoso! Grupo actual: %s\n\nAhora puede pedir a este bot que consulte a %s.\nEjemplo: \"Pregunta a %s sobre ...\"",
	},
	MsgRelaySetupHint: {
		LangEnglish:            "\n\n⚠️ This agent does not auto-inject lark-agent-bot instructions.\nPlease run `/bind setup` or `/cron setup` to write instructions to %s.",
		LangChinese:            "\n\n⚠️ 当前 agent 不会自动注入 lark-agent-bot 指令。\n请运行 `/bind setup` 或 `/cron setup` 将指令写入 %s。",
		LangTraditionalChinese: "\n\n⚠️ 當前 agent 不會自動注入 lark-agent-bot 指令。\n請執行 `/bind setup` 或 `/cron setup` 將指令寫入 %s。",
		LangJapanese:           "\n\n⚠️ このエージェントは lark-agent-bot の指示を自動注入しません。\n`/bind setup` または `/cron setup` を実行して %s に指示を書き込んでください。",
		LangSpanish:            "\n\n⚠️ Este agente no inyecta automáticamente las instrucciones de lark-agent-bot.\nEjecute `/bind setup` o `/cron setup` para escribirlas en %s.",
	},
	MsgRelaySetupOK: {
		LangEnglish:            "✅ lark-agent-bot instructions written to %s\nThe agent can now use relay, cron, and attachment send-back.",
		LangChinese:            "✅ lark-agent-bot 指令已写入 %s\nagent 现在可以使用中继、定时任务和附件回传功能了。",
		LangTraditionalChinese: "✅ lark-agent-bot 指令已寫入 %s\nagent 現在可以使用中繼、定時任務和附件回傳功能了。",
		LangJapanese:           "✅ lark-agent-bot の指示を %s に書き込みました。\nエージェントがリレー、cron、添付ファイル返送を使えるようになりました。",
		LangSpanish:            "✅ Instrucciones de lark-agent-bot escritas en %s\nEl agente ahora puede usar relay, cron y reenvío de adjuntos.",
	},
	MsgRelaySetupExists: {
		LangEnglish:            "ℹ️ lark-agent-bot instructions already exist in %s — no changes made.",
		LangChinese:            "ℹ️ lark-agent-bot 指令已存在于 %s 中，无需重复写入。",
		LangTraditionalChinese: "ℹ️ lark-agent-bot 指令已存在於 %s 中，無需重複寫入。",
		LangJapanese:           "ℹ️ lark-agent-bot の指示は既に %s に存在します。変更はありません。",
		LangSpanish:            "ℹ️ Las instrucciones de lark-agent-bot ya existen en %s — sin cambios.",
	},
	MsgRelaySetupNoMemory: {
		LangEnglish:            "❌ This agent does not support instruction files.",
		LangChinese:            "❌ 当前 agent 不支持指令文件。",
		LangTraditionalChinese: "❌ 當前 agent 不支持指令檔案。",
		LangJapanese:           "❌ このエージェントは指示ファイルをサポートしていません。",
		LangSpanish:            "❌ Este agente no soporta archivos de instrucciones.",
	},
	MsgSetupNative: {
		LangEnglish:            "✅ This agent natively supports lark-agent-bot instructions — no setup needed.",
		LangChinese:            "✅ 当前 agent 已原生支持 lark-agent-bot 指令，无需额外配置。",
		LangTraditionalChinese: "✅ 當前 agent 已原生支持 lark-agent-bot 指令，無需額外配置。",
		LangJapanese:           "✅ このエージェントは lark-agent-bot の指示をネイティブサポートしています。セットアップ不要です。",
		LangSpanish:            "✅ Este agente soporta nativamente las instrucciones de lark-agent-bot — no se necesita configuración.",
	},
	MsgCronSetupOK: {
		LangEnglish:            "✅ lark-agent-bot instructions written to %s\nThe agent can now use relay, cron, and attachment send-back.",
		LangChinese:            "✅ lark-agent-bot 指令已写入 %s\nagent 现在可以使用中继、定时任务和附件回传功能了。",
		LangTraditionalChinese: "✅ lark-agent-bot 指令已寫入 %s\nagent 現在可以使用中繼、定時任務和附件回傳功能了。",
		LangJapanese:           "✅ lark-agent-bot の指示を %s に書き込みました。\nエージェントがリレー、cron、添付ファイル返送を使えるようになりました。",
		LangSpanish:            "✅ Instrucciones de lark-agent-bot escritas en %s\nEl agente ahora puede usar relay, cron y reenvío de adjuntos.",
	},
	MsgSearchUsage: {
		LangEnglish:            "Usage: /search <keyword>\nSearch sessions by name or ID.",
		LangChinese:            "用法: /search <关键词>\n搜索会话名称或 ID。",
		LangTraditionalChinese: "用法: /search <關鍵詞>\n搜尋會話名稱或 ID。",
		LangJapanese:           "使い方: /search <キーワード>\nセッション名またはIDで検索。",
		LangSpanish:            "Uso: /search <palabra_clave>\nBuscar sesiones por nombre o ID.",
	},
	MsgSearchError: {
		LangEnglish:            "❌ Search error: %v",
		LangChinese:            "❌ 搜索失败: %v",
		LangTraditionalChinese: "❌ 搜尋失敗: %v",
		LangJapanese:           "❌ 検索エラー: %v",
		LangSpanish:            "❌ Error de búsqueda: %v",
	},
	MsgSearchNoResult: {
		LangEnglish:            "No sessions found matching %q",
		LangChinese:            "没有找到匹配 %q 的会话",
		LangTraditionalChinese: "沒有找到匹配 %q 的會話",
		LangJapanese:           "%q に一致するセッションが見つかりません",
		LangSpanish:            "No se encontraron sesiones que coincidan con %q",
	},
	MsgSearchResult: {
		LangEnglish:            "🔍 Found %d session(s) matching %q:",
		LangChinese:            "🔍 找到 %d 个匹配 %q 的会话:",
		LangTraditionalChinese: "🔍 找到 %d 個匹配 %q 的會話:",
		LangJapanese:           "🔍 %q に一致する %d 件のセッション:",
		LangSpanish:            "🔍 Se encontraron %d sesiones que coinciden con %q:",
	},
	MsgSearchHint: {
		LangEnglish:            "Use /switch <id> to switch to a session.",
		LangChinese:            "使用 /switch <id> 切换到对应会话。",
		LangTraditionalChinese: "使用 /switch <id> 切換到對應會話。",
		LangJapanese:           "/switch <id> でセッションを切り替え。",
		LangSpanish:            "Usa /switch <id> para cambiar a una sesión.",
	},
	// Builtin command descriptions
	MsgBuiltinCmdNew: {
		LangEnglish:            "Start a new session, arg: [name]",
		LangChinese:            "创建新会话，参数: [名称]",
		LangTraditionalChinese: "建立新會話，參數: [名稱]",
		LangJapanese:           "新しいセッションを開始、引数: [名前]",
		LangSpanish:            "Iniciar una nueva sesión, arg: [nombre]",
	},
	MsgBuiltinCmdList: {
		LangEnglish:            "List agent sessions",
		LangChinese:            "列出 Agent 会话列表",
		LangTraditionalChinese: "列出 Agent 會話列表",
		LangJapanese:           "エージェントセッション一覧",
		LangSpanish:            "Listar sesiones del agente",
	},
	MsgBuiltinCmdSearch: {
		LangEnglish:            "Search sessions by name or ID, arg: <keyword>",
		LangChinese:            "搜索会话名称或 ID，参数: <关键词>",
		LangTraditionalChinese: "搜尋會話名稱或 ID，參數: <關鍵詞>",
		LangJapanese:           "セッションを名前またはIDで検索、引数: <キーワード>",
		LangSpanish:            "Buscar sesiones por nombre o ID, arg: <palabra_clave>",
	},
	MsgBuiltinCmdSwitch: {
		LangEnglish:            "Resume a session by its list number, arg: <number>",
		LangChinese:            "按列表序号切换会话，参数: <序号>",
		LangTraditionalChinese: "按列表序號切換會話，參數: <序號>",
		LangJapanese:           "リスト番号でセッションを切り替え、引数: <番号>",
		LangSpanish:            "Reanudar sesión por su número en la lista, arg: <número>",
	},
	MsgBuiltinCmdDelete: {
		LangEnglish:            "Delete session(s) by list number, args: <number> | 1,2,3 | 3-7 | 1,3-5,8",
		LangChinese:            "按列表序号删除会话，参数: <序号> | 1,2,3 | 3-7 | 1,3-5,8",
		LangTraditionalChinese: "按列表序號刪除會話，參數: <序號> | 1,2,3 | 3-7 | 1,3-5,8",
		LangJapanese:           "リスト番号でセッションを削除、引数: <番号> | 1,2,3 | 3-7 | 1,3-5,8",
		LangSpanish:            "Eliminar sesión(es) por número de lista, args: <número> | 1,2,3 | 3-7 | 1,3-5,8",
	},
	MsgBuiltinCmdName: {
		LangEnglish:            "Name a session for easy identification, arg: [number] <text>",
		LangChinese:            "给会话命名，方便识别，参数: [序号] <名称>",
		LangTraditionalChinese: "為會話命名，方便辨識，參數: [序號] <名稱>",
		LangJapanese:           "セッションに名前を付ける、引数: [番号] <名前>",
		LangSpanish:            "Nombrar una sesión para fácil identificación, arg: [número] <texto>",
	},
	MsgBuiltinCmdCurrent: {
		LangEnglish:            "Show current active session",
		LangChinese:            "查看当前活跃会话",
		LangTraditionalChinese: "查看當前活躍會話",
		LangJapanese:           "現在のアクティブセッションを表示",
		LangSpanish:            "Mostrar sesión activa actual",
	},
	MsgBuiltinCmdHistory: {
		LangEnglish:            "Show last n messages, arg: [n] (default 10)",
		LangChinese:            "查看最近 n 条消息，参数: [n]（默认 10）",
		LangTraditionalChinese: "查看最近 n 條訊息，參數: [n]（預設 10）",
		LangJapanese:           "直近 n 件のメッセージを表示、引数: [n]（デフォルト 10）",
		LangSpanish:            "Mostrar últimos n mensajes, arg: [n] (por defecto 10)",
	},
	MsgBuiltinCmdProvider: {
		LangEnglish:            "Manage API providers, arg: [list|add|remove|switch|clear]",
		LangChinese:            "管理 API Provider，参数: [list|add|remove|switch|clear]",
		LangTraditionalChinese: "管理 API Provider，參數: [list|add|remove|switch|clear]",
		LangJapanese:           "API プロバイダ管理、引数: [list|add|remove|switch|clear]",
		LangSpanish:            "Gestionar proveedores API, arg: [list|add|remove|switch|clear]",
	},
	MsgBuiltinCmdMemory: {
		LangEnglish:            "View/edit agent memory files, arg: [add|global|global add]",
		LangChinese:            "查看/编辑 Agent 记忆文件，参数: [add|global|global add]",
		LangTraditionalChinese: "查看/編輯 Agent 記憶檔案，參數: [add|global|global add]",
		LangJapanese:           "エージェントメモリの表示/編集、引数: [add|global|global add]",
		LangSpanish:            "Ver/editar archivos de memoria del agente, arg: [add|global|global add]",
	},
	MsgBuiltinCmdAllow: {
		LangEnglish:            "Pre-allow a tool (next session), arg: <tool>",
		LangChinese:            "预授权工具（下次会话生效），参数: <工具名>",
		LangTraditionalChinese: "預授權工具（下次會話生效），參數: <工具名>",
		LangJapanese:           "ツールを事前許可（次のセッションで有効）、引数: <ツール>",
		LangSpanish:            "Pre-autorizar herramienta (próxima sesión), arg: <herramienta>",
	},
	MsgBuiltinCmdModel: {
		LangEnglish:            "View/switch model, arg: [name]",
		LangChinese:            "查看/切换模型，参数: [名称]",
		LangTraditionalChinese: "查看/切換模型，參數: [名稱]",
		LangJapanese:           "モデルの表示/切り替え、引数: [名前]",
		LangSpanish:            "Ver/cambiar modelo, arg: [nombre]",
	},
	MsgBuiltinCmdReasoning: {
		LangEnglish:            "View/switch reasoning effort, arg: [level]",
		LangChinese:            "查看/切换推理强度，参数: [等级]",
		LangTraditionalChinese: "查看/切換推理強度，參數: [等級]",
		LangJapanese:           "推論強度の表示/切り替え、引数: [レベル]",
		LangSpanish:            "Ver/cambiar esfuerzo de razonamiento, arg: [nivel]",
	},
	MsgBuiltinCmdMode: {
		LangEnglish:            "View/switch permission mode, arg: [name]",
		LangChinese:            "查看/切换权限模式，参数: [名称]",
		LangTraditionalChinese: "查看/切換權限模式，參數: [名稱]",
		LangJapanese:           "権限モードの表示/切り替え、引数: [名前]",
		LangSpanish:            "Ver/cambiar modo de permisos, arg: [nombre]",
	},
	MsgBuiltinCmdLang: {
		LangEnglish:            "View/switch language, arg: [en|zh|zh-TW|ja|es|auto]",
		LangChinese:            "查看/切换语言，参数: [en|zh|zh-TW|ja|es|auto]",
		LangTraditionalChinese: "查看/切換語言，參數: [en|zh|zh-TW|ja|es|auto]",
		LangJapanese:           "言語の表示/切り替え、引数: [en|zh|zh-TW|ja|es|auto]",
		LangSpanish:            "Ver/cambiar idioma, arg: [en|zh|zh-TW|ja|es|auto]",
	},
	MsgBuiltinCmdQuiet: {
		LangEnglish:            "Toggle thinking/tool progress, arg: [global]",
		LangChinese:            "开关思考和工具进度消息, 参数: [global]",
		LangTraditionalChinese: "開關思考和工具進度訊息, 參數: [global]",
		LangJapanese:           "思考/ツール進捗メッセージの表示切替, 引数: [global]",
		LangSpanish:            "Alternar mensajes de progreso, arg: [global]",
	},
	MsgBuiltinCmdCompress: {
		LangEnglish:            "Compress conversation context",
		LangChinese:            "压缩会话上下文",
		LangTraditionalChinese: "壓縮會話上下文",
		LangJapanese:           "会話コンテキストを圧縮",
		LangSpanish:            "Comprimir contexto de conversación",
	},
	MsgBuiltinCmdStop: {
		LangEnglish:            "Stop current execution",
		LangChinese:            "停止当前执行",
		LangTraditionalChinese: "停止當前執行",
		LangJapanese:           "現在の実行を停止",
		LangSpanish:            "Detener ejecución actual",
	},
	MsgBuiltinCmdCron: {
		LangEnglish:            "Manage scheduled tasks, arg: [add|list|exec|del|enable|disable]",
		LangChinese:            "管理定时任务，参数: [add|list|exec|del|enable|disable]",
		LangTraditionalChinese: "管理定時任務，參數: [add|list|exec|del|enable|disable]",
		LangJapanese:           "スケジュールタスク管理、引数: [add|list|exec|del|enable|disable]",
		LangSpanish:            "Gestionar tareas programadas, arg: [add|list|exec|del|enable|disable]",
	},
	MsgBuiltinCmdCommands: {
		LangEnglish:            "Manage custom slash commands, arg: [add|del]",
		LangChinese:            "管理自定义命令，参数: [add|del]",
		LangTraditionalChinese: "管理自訂命令，參數: [add|del]",
		LangJapanese:           "カスタムコマンド管理、引数: [add|del]",
		LangSpanish:            "Gestionar comandos personalizados, arg: [add|del]",
	},
	MsgBuiltinCmdAlias: {
		LangEnglish:            "Manage command aliases, arg: [add|del]",
		LangChinese:            "管理命令别名，参数: [add|del]",
		LangTraditionalChinese: "管理命令別名，參數: [add|del]",
		LangJapanese:           "コマンドエイリアス管理、引数: [add|del]",
		LangSpanish:            "Gestionar alias de comandos, arg: [add|del]",
	},
	MsgBuiltinCmdSkills: {
		LangEnglish:            "List agent skills (from SKILL.md)",
		LangChinese:            "列出 Agent Skills（来自 SKILL.md）",
		LangTraditionalChinese: "列出 Agent Skills（來自 SKILL.md）",
		LangJapanese:           "エージェントスキル一覧（SKILL.md から）",
		LangSpanish:            "Listar skills del agente (desde SKILL.md)",
	},
	MsgBuiltinCmdConfig: {
		LangEnglish:            "View/update runtime configuration, arg: [get|set|reload] [key] [value]",
		LangChinese:            "查看/修改运行时配置，参数: [get|set|reload] [键] [值]",
		LangTraditionalChinese: "查看/修改執行階段配置，參數: [get|set|reload] [鍵] [值]",
		LangJapanese:           "ランタイム設定の表示/変更、引数: [get|set|reload] [キー] [値]",
		LangSpanish:            "Ver/actualizar configuración en tiempo de ejecución, arg: [get|set|reload] [clave] [valor]",
	},
	MsgBuiltinCmdDoctor: {
		LangEnglish:            "Run system diagnostics",
		LangChinese:            "运行系统诊断",
		LangTraditionalChinese: "執行系統診斷",
		LangJapanese:           "システム診断を実行",
		LangSpanish:            "Ejecutar diagnósticos del sistema",
	},
	MsgBuiltinCmdUpgrade: {
		LangEnglish:            "Check for updates and self-update",
		LangChinese:            "检查更新并自动升级",
		LangTraditionalChinese: "檢查更新並自動升級",
		LangJapanese:           "アップデートを確認して自動更新",
		LangSpanish:            "Buscar actualizaciones y auto-actualizar",
	},
	MsgBuiltinCmdRestart: {
		LangEnglish:            "Restart lark-agent-bot service",
		LangChinese:            "重启 lark-agent-bot 服务",
		LangTraditionalChinese: "重啟 lark-agent-bot 服務",
		LangJapanese:           "lark-agent-bot サービスを再起動",
		LangSpanish:            "Reiniciar el servicio lark-agent-bot",
	},
	MsgBuiltinCmdStatus: {
		LangEnglish:            "Show system status",
		LangChinese:            "查看系统状态",
		LangTraditionalChinese: "查看系統狀態",
		LangJapanese:           "システム状態を表示",
		LangSpanish:            "Mostrar estado del sistema",
	},
	MsgBuiltinCmdUsage: {
		LangEnglish:            "Show account/model quota usage",
		LangChinese:            "查看账号/模型限额使用情况",
		LangTraditionalChinese: "查看帳號/模型限額使用情況",
		LangJapanese:           "アカウント/モデル使用量を表示",
		LangSpanish:            "Mostrar uso de cuota de cuenta/modelo",
	},
	MsgBuiltinCmdVersion: {
		LangEnglish:            "Show lark-agent-bot version",
		LangChinese:            "查看 lark-agent-bot 版本",
		LangTraditionalChinese: "查看 lark-agent-bot 版本",
		LangJapanese:           "lark-agent-bot のバージョンを表示",
		LangSpanish:            "Mostrar versión de lark-agent-bot",
	},
	MsgBuiltinCmdHelp: {
		LangEnglish:            "Show this help",
		LangChinese:            "显示此帮助",
		LangTraditionalChinese: "顯示此說明",
		LangJapanese:           "このヘルプを表示",
		LangSpanish:            "Mostrar esta ayuda",
	},
	MsgBuiltinCmdBind: {
		LangEnglish:            "Bind current session to a target, arg: <target>",
		LangChinese:            "绑定当前会话到目标，参数: <目标>",
		LangTraditionalChinese: "綁定當前會話到目標，參數: <目標>",
		LangJapanese:           "現在のセッションをターゲットにバインド、引数: <ターゲット>",
		LangSpanish:            "Vincular sesión actual a un objetivo, arg: <objetivo>",
	},
	MsgBuiltinCmdShell: {
		LangEnglish:            "Run a shell command, arg: <command>",
		LangChinese:            "执行 Shell 命令，参数: <命令>",
		LangTraditionalChinese: "執行 Shell 命令，參數: <命令>",
		LangJapanese:           "シェルコマンドを実行、引数: <コマンド>",
		LangSpanish:            "Ejecutar un comando shell, arg: <comando>",
	},
	MsgBuiltinCmdDir: {
		LangEnglish:            "Show, switch, or reset agent working directory, arg: <path>",
		LangChinese:            "查看、切换或重置 Agent 工作目录，参数: <路径>",
		LangTraditionalChinese: "查看、切換或重置 Agent 工作目錄，參數: <路徑>",
		LangJapanese:           "エージェントの作業ディレクトリを表示/変更/リセット、引数: <パス>",
		LangSpanish:            "Ver, cambiar o restablecer el directorio de trabajo del agente, arg: <ruta>",
	},
	MsgBuiltinCmdDiff: {
		LangEnglish:            "Generate git diff as HTML file, arg: [target]",
		LangChinese:            "生成 git diff 并以 HTML 文件发送，参数: [目标]",
		LangTraditionalChinese: "產生 git diff 並以 HTML 檔案傳送，參數: [目標]",
		LangJapanese:           "git diff を HTML ファイルで生成、引数: [ターゲット]",
		LangSpanish:            "Generar git diff como archivo HTML, arg: [objetivo]",
	},
	MsgBuiltinCmdPs: {
		LangEnglish:            "Send a P.S. to the running task",
		LangChinese:            "向正在执行的任务追加补充信息",
		LangTraditionalChinese: "向正在執行的任務追加補充資訊",
		LangJapanese:           "実行中のタスクに補足情報を送信",
		LangSpanish:            "Enviar un P.S. a la tarea en curso",
	},
	MsgDiffEmpty: {
		LangEnglish:            "No diff — clean working tree (or no changes vs `%s`).",
		LangChinese:            "无差异 — 工作区干净（或与 `%s` 无变化）。",
		LangTraditionalChinese: "無差異 — 工作區乾淨（或與 `%s` 無變化）。",
		LangJapanese:           "差分なし — 作業ツリーはクリーン（または `%s` との差分なし）。",
		LangSpanish:            "Sin diferencias — árbol limpio (o sin cambios vs `%s`).",
	},
	MsgDiffNoDiff2HTML: {
		LangEnglish:            "`diff2html` is not installed, sending plain text diff.\nInstall: `npm install -g diff2html-cli`",
		LangChinese:            "未安装 `diff2html`，将以纯文本发送差异。\n安装命令: `npm install -g diff2html-cli`",
		LangTraditionalChinese: "未安裝 `diff2html`，將以純文字傳送差異。\n安裝指令: `npm install -g diff2html-cli`",
		LangJapanese:           "`diff2html` がインストールされていません。プレーンテキストで差分を送信します。\nインストール: `npm install -g diff2html-cli`",
		LangSpanish:            "`diff2html` no está instalado, enviando diff en texto plano.\nInstalar: `npm install -g diff2html-cli`",
	},
	MsgDirChanged: {
		LangEnglish:            "✅ Work directory changed to: `%s`\nThe next session will start in this directory.",
		LangChinese:            "✅ 工作目录已切换为: `%s`\n下次会话将在此目录下启动。",
		LangTraditionalChinese: "✅ 工作目錄已切換為: `%s`\n下次會話將在此目錄下啟動。",
		LangJapanese:           "✅ 作業ディレクトリを変更しました: `%s`\n次のセッションはこのディレクトリで起動します。",
		LangSpanish:            "✅ Directorio de trabajo cambiado a: `%s`\nLa próxima sesión iniciará en este directorio.",
	},
	MsgDirCurrent: {
		LangEnglish:            "📂 Current work directory: `%s`",
		LangChinese:            "📂 当前工作目录: `%s`",
		LangTraditionalChinese: "📂 當前工作目錄: `%s`",
		LangJapanese:           "📂 現在の作業ディレクトリ: `%s`",
		LangSpanish:            "📂 Directorio de trabajo actual: `%s`",
	},
	MsgDirReset: {
		LangEnglish:            "✅ Work directory reset to the configured default: `%s`",
		LangChinese:            "✅ 工作目录已重置为配置的默认目录: `%s`",
		LangTraditionalChinese: "✅ 工作目錄已重置為設定的預設目錄: `%s`",
		LangJapanese:           "✅ 作業ディレクトリを設定済みのデフォルトに戻しました: `%s`",
		LangSpanish:            "✅ El directorio de trabajo se restauró al valor predeterminado configurado: `%s`",
	},
	MsgDirUsage: {
		LangEnglish:            "Usage: `/dir <path>`\n       `/dir reset`\nExample: `/dir ../project`",
		LangChinese:            "用法: `/dir <路径>`\n      `/dir reset`\n示例: `/dir ../project`",
		LangTraditionalChinese: "用法: `/dir <路徑>`\n      `/dir reset`\n範例: `/dir ../project`",
		LangJapanese:           "使い方: `/dir <パス>`\n       `/dir reset`\n例: `/dir ../project`",
		LangSpanish:            "Uso: `/dir <ruta>`\n      `/dir reset`\nEjemplo: `/dir ../project`",
	},
	MsgDirNotSupported: {
		LangEnglish:            "This agent does not support dynamic work directory switching.",
		LangChinese:            "当前 Agent 不支持动态切换工作目录。",
		LangTraditionalChinese: "當前 Agent 不支援動態切換工作目錄。",
		LangJapanese:           "このエージェントは動的な作業ディレクトリの切り替えをサポートしていません。",
		LangSpanish:            "Este agente no soporta el cambio dinámico de directorio de trabajo.",
	},
	MsgDirInvalidPath: {
		LangEnglish:            "❌ Directory does not exist: `%s`",
		LangChinese:            "❌ 目录不存在: `%s`",
		LangTraditionalChinese: "❌ 目錄不存在: `%s`",
		LangJapanese:           "❌ ディレクトリが存在しません: `%s`",
		LangSpanish:            "❌ El directorio no existe: `%s`",
	},
	MsgDirHistoryTitle: {
		LangEnglish:            "📋 History:",
		LangChinese:            "📋 历史记录:",
		LangTraditionalChinese: "📋 歷史記錄:",
		LangJapanese:           "📋 履歴:",
		LangSpanish:            "📋 Historial:",
	},
	MsgDirHistoryHint: {
		LangEnglish:            "💡 Use `/dir <number>` to switch, or `/dir -` for previous.",
		LangChinese:            "💡 使用 `/dir <序号>` 切换，或 `/dir -` 返回上一个目录。",
		LangTraditionalChinese: "💡 使用 `/dir <序號>` 切換，或 `/dir -` 返回上一個目錄。",
		LangJapanese:           "💡 `/dir <番号>` で切り替え、`/dir -` で前のディレクトリに戻ります。",
		LangSpanish:            "💡 Usa `/dir <número>` para cambiar, o `/dir -` para el anterior.",
	},
	MsgDirInvalidIndex: {
		LangEnglish:            "❌ Invalid history index: %d",
		LangChinese:            "❌ 无效的历史序号: %d",
		LangTraditionalChinese: "❌ 無效的歷史序號: %d",
		LangJapanese:           "❌ 無効な履歴番号: %d",
		LangSpanish:            "❌ Índice de historial inválido: %d",
	},
	MsgDirNoHistory: {
		LangEnglish:            "❌ No directory history available.",
		LangChinese:            "❌ 暂无目录历史记录。",
		LangTraditionalChinese: "❌ 暫無目錄歷史記錄。",
		LangJapanese:           "❌ ディレクトリの履歴がありません。",
		LangSpanish:            "❌ No hay historial de directorios.",
	},
	MsgDirNoPrevious: {
		LangEnglish:            "❌ No previous directory in history.",
		LangChinese:            "❌ 没有上一个目录记录。",
		LangTraditionalChinese: "❌ 沒有上一個目錄記錄。",
		LangJapanese:           "❌ 前のディレクトリが履歴にありません。",
		LangSpanish:            "❌ No hay directorio anterior en el historial.",
	},
	MsgDirCardTitle: {
		LangEnglish:            "Working directory",
		LangChinese:            "工作目录",
		LangTraditionalChinese: "工作目錄",
		LangJapanese:           "作業ディレクトリ",
		LangSpanish:            "Directorio de trabajo",
	},
	MsgDirCardPageHint: {
		LangEnglish:            "Page %d/%d — use `/dir <page>` or the buttons below.",
		LangChinese:            "第 %d/%d 页 — 可用 `/dir <页码>` 或下方按钮翻页。",
		LangTraditionalChinese: "第 %d/%d 頁 — 可用 `/dir <頁碼>` 或下方按鈕翻頁。",
		LangJapanese:           "%d/%d ページ — `/dir <ページ>` または下のボタンで移動。",
		LangSpanish:            "Página %d/%d — usa `/dir <página>` o los botones.",
	},
	MsgDirCardEmptyHistory: {
		LangEnglish:            "No directory history yet. Type `/dir <path>` to switch, or use **Reset** to restore the default.",
		LangChinese:            "暂无目录历史。可发送 `/dir <路径>` 切换，或点 **重置** 恢复默认目录。",
		LangTraditionalChinese: "暫無目錄歷史。可傳送 `/dir <路徑>` 切換，或點 **重置** 恢復預設目錄。",
		LangJapanese:           "まだディレクトリ履歴がありません。`/dir <パス>` で切替えるか、**リセット** で既定に戻せます。",
		LangSpanish:            "Aún no hay historial de directorios. Usa `/dir <ruta>` o **Restablecer** al valor por defecto.",
	},
	MsgDirCardReset: {
		LangEnglish:            "Reset",
		LangChinese:            "重置",
		LangTraditionalChinese: "重置",
		LangJapanese:           "リセット",
		LangSpanish:            "Restablecer",
	},
	MsgDirCardPrev: {
		LangEnglish:            "Previous",
		LangChinese:            "上一目录",
		LangTraditionalChinese: "上一目錄",
		LangJapanese:           "前へ",
		LangSpanish:            "Anterior",
	},
	MsgShow: {
		LangEnglish:            "View file / directory / snippet by reference",
		LangChinese:            "按引用查看文件、目录或代码片段",
		LangTraditionalChinese: "按引用查看檔案、目錄或程式碼片段",
		LangJapanese:           "参照からファイル・ディレクトリ・コード断片を表示",
		LangSpanish:            "Ver archivo/directorio/fragmento por referencia",
	},
	MsgShowUsage: {
		LangEnglish:            "Usage: `/show <path|path:line|path:start-end|dir/>`\nExample: `/show svc/recovery_session_reconciler.go:12`",
		LangChinese:            "用法: `/show <路径|路径:行号|路径:起止行|目录/>`\n示例: `/show svc/recovery_session_reconciler.go:12`",
		LangTraditionalChinese: "用法: `/show <路徑|路徑:行號|路徑:起止行|目錄/>`\n範例: `/show svc/recovery_session_reconciler.go:12`",
		LangJapanese:           "使い方: `/show <パス|パス:行|パス:開始-終了|dir/>`\n例: `/show svc/recovery_session_reconciler.go:12`",
		LangSpanish:            "Uso: `/show <ruta|ruta:línea|ruta:inicio-fin|dir/>`\nEjemplo: `/show svc/recovery_session_reconciler.go:12`",
	},
	MsgShowParseError: {
		LangEnglish:            "❌ Cannot parse reference: `%s`",
		LangChinese:            "❌ 无法解析引用: `%s`",
		LangTraditionalChinese: "❌ 無法解析引用: `%s`",
		LangJapanese:           "❌ 参照を解析できません: `%s`",
		LangSpanish:            "❌ No se puede interpretar la referencia: `%s`",
	},
	MsgShowNotFound: {
		LangEnglish:            "❌ Referenced path does not exist: `%s`",
		LangChinese:            "❌ 引用路径不存在: `%s`",
		LangTraditionalChinese: "❌ 引用路徑不存在: `%s`",
		LangJapanese:           "❌ 参照パスが存在しません: `%s`",
		LangSpanish:            "❌ La ruta referenciada no existe: `%s`",
	},
	MsgShowDirWithLocation: {
		LangEnglish:            "❌ Directory references cannot include line information: `%s`",
		LangChinese:            "❌ 目录引用不能带行号信息: `%s`",
		LangTraditionalChinese: "❌ 目錄引用不能帶行號資訊: `%s`",
		LangJapanese:           "❌ ディレクトリ参照に行情報は指定できません: `%s`",
		LangSpanish:            "❌ Una referencia de directorio no puede incluir líneas: `%s`",
	},
	MsgShowReadFailed: {
		LangEnglish:            "❌ Failed to read reference: %s",
		LangChinese:            "❌ 读取引用失败: %s",
		LangTraditionalChinese: "❌ 讀取引用失敗: %s",
		LangJapanese:           "❌ 参照の読み取りに失敗しました: %s",
		LangSpanish:            "❌ Error al leer la referencia: %s",
	},

	// Multi-workspace messages
	MsgBuiltinCmdWorkspace: {
		LangEnglish:            "Show the current project or choose a workspace",
		LangChinese:            "查看当前项目或选择工作区",
		LangTraditionalChinese: "查看目前專案或選擇工作區",
		LangJapanese:           "現在のプロジェクトを表示、またはワークスペースを選択",
		LangSpanish:            "Ver el proyecto actual o elegir un espacio de trabajo",
	},
	MsgWsPickerDescription: {
		LangEnglish:            "Choose a project to bind to this chat",
		LangChinese:            "选择项目并绑定当前聊天",
		LangTraditionalChinese: "選擇專案並綁定目前聊天",
		LangJapanese:           "このチャットに紐づけるプロジェクトを選択",
		LangSpanish:            "Elegir un proyecto para vincular a este chat",
	},
	MsgWsPickerTitle: {
		LangEnglish:            "Choose project (%d) · %d/%d",
		LangChinese:            "选择项目（%d）· %d/%d",
		LangTraditionalChinese: "選擇專案（%d）· %d/%d",
		LangJapanese:           "プロジェクトを選択 (%d) · %d/%d",
		LangSpanish:            "Elegir proyecto (%d) · %d/%d",
	},
	MsgWsPickerRoot: {
		LangEnglish:            "Project root: `%s`",
		LangChinese:            "项目根目录：`%s`",
		LangTraditionalChinese: "專案根目錄：`%s`",
		LangJapanese:           "プロジェクトの親フォルダー: `%s`",
		LangSpanish:            "Carpeta de proyectos: `%s`",
	},
	MsgWsPickerCurrent: {
		LangEnglish:            "Currently bound: `%s`",
		LangChinese:            "当前绑定：`%s`",
		LangTraditionalChinese: "目前綁定：`%s`",
		LangJapanese:           "現在の紐づけ先: `%s`",
		LangSpanish:            "Vinculado actualmente: `%s`",
	},
	MsgWsPickerEmpty: {
		LangEnglish:            "No project directories found under this root.",
		LangChinese:            "此根目录下暂无可选的项目文件夹。",
		LangTraditionalChinese: "此根目錄下暫無可選的專案資料夾。",
		LangJapanese:           "この親フォルダーに選択可能なプロジェクトがありません。",
		LangSpanish:            "No hay carpetas de proyectos disponibles aquí.",
	},
	MsgWsPickerSelect: {
		LangEnglish:            "Bind",
		LangChinese:            "绑定",
		LangTraditionalChinese: "綁定",
		LangJapanese:           "紐づける",
		LangSpanish:            "Vincular",
	},
	MsgWsPickerSelected: {
		LangEnglish:            "Current",
		LangChinese:            "当前项目",
		LangTraditionalChinese: "目前專案",
		LangJapanese:           "現在のプロジェクト",
		LangSpanish:            "Actual",
	},
	MsgWsPickerHint: {
		LangEnglish:            "Select a project for this chat. Text command: /workspace bind <folder>. /workspace available [page] lists folders; /bind <bot> still manages bot relay bindings.",
		LangChinese:            "选择后绑定当前聊天。也可发送 /workspace bind <文件夹名>；/workspace available [页码] 浏览目录。/bind <机器人名> 仍用于机器人中继绑定。",
		LangTraditionalChinese: "選擇後綁定目前聊天。也可傳送 /workspace bind <資料夾名>；/workspace available [頁碼] 瀏覽目錄。/bind <機器人名> 仍用於機器人中繼綁定。",
		LangJapanese:           "このチャットのプロジェクトを選択。/workspace bind <フォルダー> でも指定できます。/workspace available [ページ] で一覧、/bind <ボット> でボット間の中継を設定します。",
		LangSpanish:            "Elija un proyecto para este chat o use /workspace bind <carpeta>. /workspace available [página] muestra carpetas; /bind <bot> mantiene los enlaces entre bots.",
	},
	MsgWsPickerStale: {
		LangEnglish:            "This project is no longer available. Use /bind to refresh the list.",
		LangChinese:            "该项目已不可选，请发送 /bind 刷新项目列表。",
		LangTraditionalChinese: "該專案已不可選，請傳送 /bind 重新整理專案列表。",
		LangJapanese:           "このプロジェクトは選択できません。/bind で一覧を更新してください。",
		LangSpanish:            "Este proyecto ya no está disponible. Use /bind para actualizar la lista.",
	},
	MsgWsNotEnabled: {
		LangEnglish:            "Workspace commands are only available in multi-workspace mode.",
		LangChinese:            "工作区命令仅在多工作区模式下可用。",
		LangTraditionalChinese: "工作區命令僅在多工作區模式下可用。",
		LangJapanese:           "ワークスペースコマンドはマルチワークスペースモードでのみ使用できます。",
		LangSpanish:            "Los comandos de workspace solo están disponibles en modo multi-workspace.",
	},
	MsgWsNoBinding: {
		LangEnglish:            "No workspace bound to this channel.",
		LangChinese:            "此频道未绑定工作区。",
		LangTraditionalChinese: "此頻道未綁定工作區。",
		LangJapanese:           "このチャンネルにワークスペースがバインドされていません。",
		LangSpanish:            "No hay workspace vinculado a este canal.",
	},
	MsgWsInfo: {
		LangEnglish:            "Workspace: `%s`\nBound: %s",
		LangChinese:            "工作区: `%s`\n绑定时间: %s",
		LangTraditionalChinese: "工作區: `%s`\n綁定時間: %s",
		LangJapanese:           "ワークスペース: `%s`\nバインド: %s",
		LangSpanish:            "Workspace: `%s`\nVinculado: %s",
	},
	MsgWsInfoShared: {
		LangEnglish:            "Workspace: `%s`\nBound: %s\nSource: shared",
		LangChinese:            "工作区: `%s`\n绑定时间: %s\n来源: shared",
		LangTraditionalChinese: "工作區: `%s`\n綁定時間: %s\n來源: shared",
		LangJapanese:           "ワークスペース: `%s`\nバインド: %s\nソース: shared",
		LangSpanish:            "Workspace: `%s`\nVinculado: %s\nOrigen: shared",
	},
	MsgWsUsage: {
		LangEnglish:            "Usage: `/workspace [bind <name> | route <absolute-path> | init <url> | unbind | list | worktree ... | shared ...]`",
		LangChinese:            "用法: `/workspace [bind <名称> | route <绝对路径> | init <仓库地址> | unbind | list | worktree ... | shared ...]`",
		LangTraditionalChinese: "用法: `/workspace [bind <名稱> | route <絕對路徑> | init <倉庫地址> | unbind | list | worktree ... | shared ...]`",
		LangJapanese:           "使い方: `/workspace [bind <名前> | route <絶対パス> | init <url> | unbind | list | worktree ... | shared ...]`",
		LangSpanish:            "Uso: `/workspace [bind <nombre> | route <ruta-absoluta> | init <url> | unbind | list | worktree ... | shared ...]`",
	},
	MsgWsInitUsage: {
		LangEnglish:            "Usage: `/workspace init <git-url or directory-path>`",
		LangChinese:            "用法: `/workspace init <git仓库地址或目录路径>`",
		LangTraditionalChinese: "用法: `/workspace init <git倉庫地址或目錄路徑>`",
		LangJapanese:           "使い方: `/workspace init <git-urlまたはディレクトリパス>`",
		LangSpanish:            "Uso: `/workspace init <git-url o ruta-de-directorio>`",
	},
	MsgWsBindUsage: {
		LangEnglish:            "Usage: `/workspace bind <workspace-name>`",
		LangChinese:            "用法: `/workspace bind <工作区名称>`",
		LangTraditionalChinese: "用法: `/workspace bind <工作區名稱>`",
		LangJapanese:           "使い方: `/workspace bind <ワークスペース名>`",
		LangSpanish:            "Uso: `/workspace bind <nombre-workspace>`",
	},
	MsgWsBindSuccess: {
		LangEnglish:            "✅ Workspace bound: `%s`",
		LangChinese:            "✅ 工作区绑定成功: `%s`",
		LangTraditionalChinese: "✅ 工作區綁定成功: `%s`",
		LangJapanese:           "✅ ワークスペースをバインドしました: `%s`",
		LangSpanish:            "✅ Workspace vinculado: `%s`",
	},
	MsgWsBindNotFound: {
		LangEnglish:            "Workspace not found: `%s`",
		LangChinese:            "工作区不存在: `%s`",
		LangTraditionalChinese: "工作區不存在: `%s`",
		LangJapanese:           "ワークスペースが見つかりません: `%s`",
		LangSpanish:            "Workspace no encontrado: `%s`",
	},
	MsgWsRouteUsage: {
		LangEnglish:            "Usage: `/workspace route <absolute-path>`",
		LangChinese:            "用法: `/workspace route <绝对路径>`",
		LangTraditionalChinese: "用法: `/workspace route <絕對路徑>`",
		LangJapanese:           "使い方: `/workspace route <絶対パス>`",
		LangSpanish:            "Uso: `/workspace route <ruta-absoluta>`",
	},
	MsgWsRouteSuccess: {
		LangEnglish:            "✅ Workspace routed: `%s`",
		LangChinese:            "✅ 工作区路由成功: `%s`",
		LangTraditionalChinese: "✅ 工作區路由成功: `%s`",
		LangJapanese:           "✅ ワークスペースをルーティングしました: `%s`",
		LangSpanish:            "✅ Workspace enrutado: `%s`",
	},
	MsgWsRouteAbsoluteRequired: {
		LangEnglish:            "Workspace route must use an absolute path: `%s`",
		LangChinese:            "工作区路由必须使用绝对路径: `%s`",
		LangTraditionalChinese: "工作區路由必須使用絕對路徑: `%s`",
		LangJapanese:           "ワークスペースの route には絶対パスが必要です: `%s`",
		LangSpanish:            "La ruta del workspace debe ser absoluta: `%s`",
	},
	MsgWsRouteNotFound: {
		LangEnglish:            "Workspace path not found: `%s`",
		LangChinese:            "工作区路径不存在: `%s`",
		LangTraditionalChinese: "工作區路徑不存在: `%s`",
		LangJapanese:           "ワークスペースのパスが見つかりません: `%s`",
		LangSpanish:            "Ruta de workspace no encontrada: `%s`",
	},
	MsgWsRouteNotDirectory: {
		LangEnglish:            "Workspace route target is not a directory: `%s`",
		LangChinese:            "工作区路由目标不是目录: `%s`",
		LangTraditionalChinese: "工作區路由目標不是目錄: `%s`",
		LangJapanese:           "ワークスペースの route 先がディレクトリではありません: `%s`",
		LangSpanish:            "El destino de workspace route no es un directorio: `%s`",
	},
	MsgWsUnbindSuccess: {
		LangEnglish:            "✅ Workspace unbound.",
		LangChinese:            "✅ 已解除工作区绑定。",
		LangTraditionalChinese: "✅ 已解除工作區綁定。",
		LangJapanese:           "✅ ワークスペースのバインドを解除しました。",
		LangSpanish:            "✅ Workspace desvinculado.",
	},
	MsgWsListEmpty: {
		LangEnglish:            "No workspaces bound.",
		LangChinese:            "没有绑定的工作区。",
		LangTraditionalChinese: "沒有綁定的工作區。",
		LangJapanese:           "バインドされたワークスペースがありません。",
		LangSpanish:            "No hay workspaces vinculados.",
	},
	MsgWsListTitle: {
		LangEnglish:            "Bound workspaces:",
		LangChinese:            "已绑定的工作区：",
		LangTraditionalChinese: "已綁定的工作區：",
		LangJapanese:           "バインドされたワークスペース：",
		LangSpanish:            "Workspaces vinculados:",
	},
	MsgWsSharedNoBinding: {
		LangEnglish:            "No shared workspace bound to this channel.",
		LangChinese:            "此频道未绑定共享工作区。",
		LangTraditionalChinese: "此頻道未綁定共享工作區。",
		LangJapanese:           "このチャンネルに共有ワークスペースがバインドされていません。",
		LangSpanish:            "No hay workspace compartido vinculado a este canal.",
	},
	MsgWsSharedUsage: {
		LangEnglish:            "Usage: `/workspace shared [bind <name> | route <absolute-path> | init <url> | unbind | list]`",
		LangChinese:            "用法: `/workspace shared [bind <名称> | route <绝对路径> | init <仓库地址> | unbind | list]`",
		LangTraditionalChinese: "用法: `/workspace shared [bind <名稱> | route <絕對路徑> | init <倉庫地址> | unbind | list]`",
		LangJapanese:           "使い方: `/workspace shared [bind <名前> | route <絶対パス> | init <url> | unbind | list]`",
		LangSpanish:            "Uso: `/workspace shared [bind <nombre> | route <ruta-absoluta> | init <url> | unbind | list]`",
	},
	MsgWsSharedBindSuccess: {
		LangEnglish:            "✅ Shared workspace bound: `%s`",
		LangChinese:            "✅ 共享工作区绑定成功: `%s`",
		LangTraditionalChinese: "✅ 共享工作區綁定成功: `%s`",
		LangJapanese:           "✅ 共有ワークスペースをバインドしました: `%s`",
		LangSpanish:            "✅ Workspace compartido vinculado: `%s`",
	},
	MsgWsSharedRouteSuccess: {
		LangEnglish:            "✅ Shared workspace routed: `%s`",
		LangChinese:            "✅ 共享工作区路由成功: `%s`",
		LangTraditionalChinese: "✅ 共享工作區路由成功: `%s`",
		LangJapanese:           "✅ 共有ワークスペースをルーティングしました: `%s`",
		LangSpanish:            "✅ Workspace compartido enrutado: `%s`",
	},
	MsgWsSharedUnbindSuccess: {
		LangEnglish:            "✅ Shared workspace unbound.",
		LangChinese:            "✅ 已解除共享工作区绑定。",
		LangTraditionalChinese: "✅ 已解除共享工作區綁定。",
		LangJapanese:           "✅ 共有ワークスペースのバインドを解除しました。",
		LangSpanish:            "✅ Workspace compartido desvinculado.",
	},
	MsgWsSharedListEmpty: {
		LangEnglish:            "No shared workspaces bound.",
		LangChinese:            "没有绑定的共享工作区。",
		LangTraditionalChinese: "沒有綁定的共享工作區。",
		LangJapanese:           "バインドされた共有ワークスペースがありません。",
		LangSpanish:            "No hay workspaces compartidos vinculados.",
	},
	MsgWsSharedListTitle: {
		LangEnglish:            "Shared workspaces:",
		LangChinese:            "共享工作区：",
		LangTraditionalChinese: "共享工作區：",
		LangJapanese:           "共有ワークスペース：",
		LangSpanish:            "Workspaces compartidos:",
	},
	MsgWsSharedOnlyHint: {
		LangEnglish:            "The current effective workspace comes from the shared layer. Use `/workspace shared unbind` to remove it.",
		LangChinese:            "当前生效的工作区来自 shared 层。请使用 `/workspace shared unbind` 解除绑定。",
		LangTraditionalChinese: "當前生效的工作區來自 shared 層。請使用 `/workspace shared unbind` 解除綁定。",
		LangJapanese:           "現在有効なワークスペースは shared レイヤー由来です。解除するには `/workspace shared unbind` を使用してください。",
		LangSpanish:            "El workspace efectivo actual proviene de la capa shared. Usa `/workspace shared unbind` para quitarlo.",
	},
	MsgWsNotFoundHint: {
		LangEnglish:            "No workspace found for this channel. Send a git repo URL, a local directory path, or use `/workspace init <url-or-path>`.",
		LangChinese:            "此频道未找到工作区。请发送 git 仓库地址或本地目录路径，或使用 `/workspace init <仓库地址或目录路径>`。",
		LangTraditionalChinese: "此頻道未找到工作區。請發送 git 倉庫地址或本地目錄路徑，或使用 `/workspace init <倉庫地址或目錄路徑>`。",
		LangJapanese:           "このチャンネルにワークスペースが見つかりません。git URL またはローカルディレクトリパスを送信するか、`/workspace init <urlまたはパス>` を使用してください。",
		LangSpanish:            "No se encontró workspace para este canal. Envía una URL de repo git, una ruta de directorio local, o usa `/workspace init <url-o-ruta>`.",
	},
	MsgWsNotFoundHintGitOnly: {
		LangEnglish:            "No workspace found for this channel. Send a git repo URL or use `/workspace init <git-url>`.",
		LangChinese:            "此频道未找到工作区。请发送 git 仓库地址，或使用 `/workspace init <git仓库地址>`。",
		LangTraditionalChinese: "此頻道未找到工作區。請發送 git 倉庫地址，或使用 `/workspace init <git倉庫地址>`。",
		LangJapanese:           "このチャンネルにワークスペースが見つかりません。git URL を送信するか、`/workspace init <git-url>` を使用してください。",
		LangSpanish:            "No se encontró workspace para este canal. Envía una URL de repo git o usa `/workspace init <git-url>`.",
	},
	MsgWsResolutionError: {
		LangEnglish:            "Workspace resolution error: %v",
		LangChinese:            "工作区解析错误: %v",
		LangTraditionalChinese: "工作區解析錯誤: %v",
		LangJapanese:           "ワークスペース解決エラー: %v",
		LangSpanish:            "Error de resolución de workspace: %v",
	},
	MsgWsCloneProgress: {
		LangEnglish:            "🔄 Cloning repository: %s",
		LangChinese:            "🔄 正在克隆仓库: %s",
		LangTraditionalChinese: "🔄 正在克隆倉庫: %s",
		LangJapanese:           "🔄 リポジトリをクローン中: %s",
		LangSpanish:            "🔄 Clonando repositorio: %s",
	},
	MsgWsCloneSuccess: {
		LangEnglish:            "✅ Repository cloned successfully: `%s`",
		LangChinese:            "✅ 仓库克隆成功: `%s`",
		LangTraditionalChinese: "✅ 倉庫克隆成功: `%s`",
		LangJapanese:           "✅ リポジトリのクローンに成功しました: `%s`",
		LangSpanish:            "✅ Repositorio clonado exitosamente: `%s`",
	},
	MsgWsCloneFailed: {
		LangEnglish:            "❌ Failed to clone repository: %v",
		LangChinese:            "❌ 克隆仓库失败: %v",
		LangTraditionalChinese: "❌ 克隆倉庫失敗: %v",
		LangJapanese:           "❌ リポジトリのクローンに失敗しました: %v",
		LangSpanish:            "❌ Error al clonar repositorio: %v",
	},
	MsgWsInitDirNotFound: {
		LangEnglish:            "Directory not found: `%s`. Please provide a valid directory path or a git URL.",
		LangChinese:            "目录不存在: `%s`。请提供有效的目录路径或 git 仓库地址。",
		LangTraditionalChinese: "目錄不存在: `%s`。請提供有效的目錄路徑或 git 倉庫地址。",
		LangJapanese:           "ディレクトリが見つかりません: `%s`。有効なディレクトリパスまたは git URL を指定してください。",
		LangSpanish:            "Directorio no encontrado: `%s`. Proporcione una ruta de directorio válida o una URL de git.",
	},
	MsgWsInitInvalidTarget: {
		LangEnglish:            "Please provide a git URL (e.g. `https://github.com/org/repo`) or a local directory path.",
		LangChinese:            "请提供 git 仓库地址（如 `https://github.com/org/repo`）或本地目录路径。",
		LangTraditionalChinese: "請提供 git 倉庫地址（如 `https://github.com/org/repo`）或本地目錄路徑。",
		LangJapanese:           "git URL（例: `https://github.com/org/repo`）またはローカルディレクトリパスを指定してください。",
		LangSpanish:            "Proporcione una URL de git (ej. `https://github.com/org/repo`) o una ruta de directorio local.",
	},
	MsgWsInitLocalPathsDisabled: {
		LangEnglish:            "Local directory targets are disabled for `/workspace init`. Use a git URL, or enable `workspace_init_allow_local_paths = true` for this project.",
		LangChinese:            "`/workspace init` 未启用本地目录目标。请使用 git 仓库地址，或在此项目配置 `workspace_init_allow_local_paths = true`。",
		LangTraditionalChinese: "`/workspace init` 未啟用本機目錄目標。請使用 git 倉庫地址，或在此專案配置 `workspace_init_allow_local_paths = true`。",
		LangJapanese:           "`/workspace init` ではローカルディレクトリ対象が無効です。git URL を使うか、このプロジェクトで `workspace_init_allow_local_paths = true` を有効にしてください。",
		LangSpanish:            "Los destinos de directorio local están deshabilitados para `/workspace init`. Use una URL de git o habilite `workspace_init_allow_local_paths = true` para este proyecto.",
	},
	MsgWsWorktreeUsage: {
		LangEnglish:            "Usage: `/workspace worktree [<name> | rm <name>]` (short: `/ws wt`). `<name>` creates or reuses `.worktrees/<name>` on branch `<name>` and switches this chat to it; without arguments, lists the worktrees.",
		LangChinese:            "用法: `/workspace worktree [<名称> | rm <名称>]`（简写 `/ws wt`）。带名称：新建或复用 `.worktrees/<名称>`（分支同名），并把当前聊天切过去；不带参数：列出工作树。",
		LangTraditionalChinese: "用法: `/workspace worktree [<名稱> | rm <名稱>]`（簡寫 `/ws wt`）。帶名稱：新建或沿用 `.worktrees/<名稱>`（分支同名），並把目前聊天切過去；不帶參數：列出工作樹。",
		LangJapanese:           "使い方: `/workspace worktree [<名前> | rm <名前>]`（短縮形 `/ws wt`）。名前を指定すると `.worktrees/<名前>`（同名ブランチ）を作成または再利用し、このチャットを切り替えます。引数なしでワークツリーを一覧表示します。",
		LangSpanish:            "Uso: `/workspace worktree [<nombre> | rm <nombre>]` (abreviado: `/ws wt`). Con `<nombre>` crea o reutiliza `.worktrees/<nombre>` en la rama `<nombre>` y cambia este chat a él; sin argumentos, lista los worktrees.",
	},
	MsgWsWorktreeNotRepo: {
		LangEnglish:            "The bound directory `%s` is not a usable git repository: %v",
		LangChinese:            "当前绑定的目录 `%s` 不是可用的 git 仓库: %v",
		LangTraditionalChinese: "目前綁定的目錄 `%s` 不是可用的 git 倉庫: %v",
		LangJapanese:           "バインド中のディレクトリ `%s` は利用できる git リポジトリではありません: %v",
		LangSpanish:            "El directorio vinculado `%s` no es un repositorio git utilizable: %v",
	},
	MsgWsWorktreeListTitle: {
		LangEnglish:            "Worktrees (main: `%s`):\n",
		LangChinese:            "工作树（主仓库 `%s`）:\n",
		LangTraditionalChinese: "工作樹（主倉庫 `%s`）:\n",
		LangJapanese:           "ワークツリー（メイン: `%s`）:\n",
		LangSpanish:            "Worktrees (principal: `%s`):\n",
	},
	MsgWsWorktreeMainLabel: {
		LangEnglish:            "main",
		LangChinese:            "主仓库",
		LangTraditionalChinese: "主倉庫",
		LangJapanese:           "メイン",
		LangSpanish:            "principal",
	},
	MsgWsWorktreeInvalidName: {
		LangEnglish:            "`%s` cannot be used as a branch name; choose another name.",
		LangChinese:            "`%s` 不能用作分支名，换一个名称。",
		LangTraditionalChinese: "`%s` 不能用作分支名，換一個名稱。",
		LangJapanese:           "`%s` はブランチ名に使えません。別の名前にしてください。",
		LangSpanish:            "`%s` no sirve como nombre de rama; elija otro nombre.",
	},
	MsgWsWorktreeSwitched: {
		LangEnglish:            "🌿 Switched to worktree `%s`: `%s`\nThe next message is handled there.",
		LangChinese:            "🌿 已切到工作树 `%s`: `%s`\n下一条消息在这里处理。",
		LangTraditionalChinese: "🌿 已切到工作樹 `%s`: `%s`\n下一則訊息在這裡處理。",
		LangJapanese:           "🌿 ワークツリー `%s` に切り替えました: `%s`\n次のメッセージからここで処理します。",
		LangSpanish:            "🌿 Cambiado al worktree `%s`: `%s`\nEl próximo mensaje se procesa allí.",
	},
	MsgWsWorktreePathTaken: {
		LangEnglish:            "`%s` already exists but is not a worktree of this repository.",
		LangChinese:            "`%s` 已存在，但不是这个仓库的工作树。",
		LangTraditionalChinese: "`%s` 已存在，但不是這個倉庫的工作樹。",
		LangJapanese:           "`%s` は既に存在しますが、このリポジトリのワークツリーではありません。",
		LangSpanish:            "`%s` ya existe pero no es un worktree de este repositorio.",
	},
	MsgWsWorktreeCreated: {
		LangEnglish:            "🌿 Created worktree `%s` (new branch from `%s`): `%s`\nThis chat now works there; the next message starts a new session.",
		LangChinese:            "🌿 已新建工作树 `%s`（新分支，基于 `%s`）: `%s`\n当前聊天已切过去，下一条消息在这里开始新会话。",
		LangTraditionalChinese: "🌿 已新建工作樹 `%s`（新分支，基於 `%s`）: `%s`\n目前聊天已切過去，下一則訊息在這裡開始新會話。",
		LangJapanese:           "🌿 ワークツリー `%s` を作成しました（`%s` からの新ブランチ）: `%s`\nこのチャットを切り替えました。次のメッセージから新しいセッションになります。",
		LangSpanish:            "🌿 Worktree `%s` creado (rama nueva desde `%s`): `%s`\nEste chat trabaja ahora allí; el próximo mensaje inicia una sesión nueva.",
	},
	MsgWsWorktreeCreatedExisting: {
		LangEnglish:            "🌿 Created a worktree for the existing branch `%s`: `%s`\nThis chat now works there; the next message starts a new session.",
		LangChinese:            "🌿 已为已有分支 `%s` 新建工作树: `%s`\n当前聊天已切过去，下一条消息在这里开始新会话。",
		LangTraditionalChinese: "🌿 已為既有分支 `%s` 新建工作樹: `%s`\n目前聊天已切過去，下一則訊息在這裡開始新會話。",
		LangJapanese:           "🌿 既存ブランチ `%s` のワークツリーを作成しました: `%s`\nこのチャットを切り替えました。次のメッセージから新しいセッションになります。",
		LangSpanish:            "🌿 Worktree creado para la rama existente `%s`: `%s`\nEste chat trabaja ahora allí; el próximo mensaje inicia una sesión nueva.",
	},
	MsgWsWorktreeFailed: {
		LangEnglish:            "❌ Worktree command failed: %v",
		LangChinese:            "❌ 工作树操作失败: %v",
		LangTraditionalChinese: "❌ 工作樹操作失敗: %v",
		LangJapanese:           "❌ ワークツリー操作に失敗しました: %v",
		LangSpanish:            "❌ Falló la operación de worktree: %v",
	},
	MsgWsWorktreeNotFound: {
		LangEnglish:            "No worktree named `%s`; send `/ws wt` to list them.",
		LangChinese:            "没有叫 `%s` 的工作树，发 `/ws wt` 查看。",
		LangTraditionalChinese: "沒有叫 `%s` 的工作樹，傳 `/ws wt` 查看。",
		LangJapanese:           "`%s` というワークツリーはありません。`/ws wt` で一覧を確認してください。",
		LangSpanish:            "No hay un worktree llamado `%s`; envíe `/ws wt` para listarlos.",
	},
	MsgWsWorktreeBusy: {
		LangEnglish:            "A task is still running in worktree `%s`; remove it after the task ends (or after /stop).",
		LangChinese:            "工作树 `%s` 里还有任务在运行，等它结束（或 /stop）后再删。",
		LangTraditionalChinese: "工作樹 `%s` 裡還有任務在執行，等它結束（或 /stop）後再刪。",
		LangJapanese:           "ワークツリー `%s` でタスクが実行中です。終了後（または /stop 後）に削除してください。",
		LangSpanish:            "Aún hay una tarea en el worktree `%s`; elimínelo cuando termine (o tras /stop).",
	},
	MsgWsWorktreeDirty: {
		LangEnglish:            "Worktree `%s` has uncommitted changes or new files; commit or clean them up before removing it.",
		LangChinese:            "工作树 `%s` 里有未提交的改动或新文件，先提交或清理后再删。",
		LangTraditionalChinese: "工作樹 `%s` 裡有未提交的變更或新檔案，先提交或清理後再刪。",
		LangJapanese:           "ワークツリー `%s` に未コミットの変更か新しいファイルがあります。コミットか整理をしてから削除してください。",
		LangSpanish:            "El worktree `%s` tiene cambios sin confirmar o archivos nuevos; confírmelos o límpielos antes de eliminarlo.",
	},
	MsgWsWorktreeRemoved: {
		LangEnglish:            "🗑 Removed worktree `%s`; branch `%s` is kept. Chats bound to it now use the main worktree `%s`.",
		LangChinese:            "🗑 已删除工作树 `%s`，分支 `%s` 保留。绑定到它的聊天已切回主仓库 `%s`。",
		LangTraditionalChinese: "🗑 已刪除工作樹 `%s`，分支 `%s` 保留。綁定到它的聊天已切回主倉庫 `%s`。",
		LangJapanese:           "🗑 ワークツリー `%s` を削除しました（ブランチ `%s` は残ります）。これをバインドしていたチャットはメイン `%s` に戻りました。",
		LangSpanish:            "🗑 Worktree `%s` eliminado; la rama `%s` se conserva. Los chats vinculados a él usan ahora el principal `%s`.",
	},
	MsgAgentSendToolPrompt: {
		LangEnglish: `### Send generated images, files, or voice messages back to the user
When you generate a local image or file that should be sent to the user, use:

  lark-agent-bot send --image /absolute/path/to/image.png
  lark-agent-bot send --file /absolute/path/to/report.pdf
  lark-agent-bot send --file /absolute/path/to/report.pdf --image /absolute/path/to/chart.png

You may repeat --image / --file multiple times. Use this only for generated attachments that need to be delivered to the user.
If you include --message, do not repeat the exact same sentence again in your normal reply, because your normal reply is also delivered automatically.

When sending an audio (mp3/wav/m4a/ogg/opus) or video (mp4/mov/webm) clip that should render inline as a native voice bubble or video player — instead of as a generic file download — use the dedicated flags:

  lark-agent-bot send --audio /absolute/path/to/clip.mp3
  lark-agent-bot send --video /absolute/path/to/demo.mp4

These render as native media on platforms that support it (e.g. Feishu voice bubbles). lark-agent-bot transparently transcodes audio to the platform's preferred codec (e.g. opus for Feishu). On platforms without dedicated audio/video support lark-agent-bot automatically falls back to the file-attachment path so delivery is preserved. Do NOT downgrade the user's request to --file when they explicitly asked for audio or video.

When the user explicitly asks you to synthesize speech from text, use:

  lark-agent-bot send --tts "text to speak"

After lark-agent-bot send --tts (or --audio) succeeds, reply only with NO_REPLY unless the user also asked for a visible text confirmation. This prevents sending an extra text message after the voice message.`,
		LangChinese: `### 把生成的图片、文件、语音消息回发给用户
当你生成了需要发送给用户的本地图片或文件时,使用:

  lark-agent-bot send --image /absolute/path/to/image.png
  lark-agent-bot send --file /absolute/path/to/report.pdf
  lark-agent-bot send --file /absolute/path/to/report.pdf --image /absolute/path/to/chart.png

可以重复使用 --image / --file。仅在需要把生成的附件投递到用户时使用这个命令。
如果同时使用了 --message,不要在正常回复里再说一遍完全相同的句子,因为正常回复本身也会自动发送给用户。

发送音频 (mp3/wav/m4a/ogg/opus) 或视频 (mp4/mov/webm) 片段、且希望它们以内联的原生语音气泡或视频播放器形态呈现(而不是作为普通文件下载)时,使用专用参数:

  lark-agent-bot send --audio /absolute/path/to/clip.mp3
  lark-agent-bot send --video /absolute/path/to/demo.mp4

这些参数会在支持原生媒体的平台上渲染为原生形态(例如飞书的语音气泡)。lark-agent-bot 会自动把音频转码为平台偏好的编码(例如飞书的 opus)。在不支持专用音视频的平台,lark-agent-bot 会自动回退到文件附件路径以保证投递成功。当用户明确要求 audio/video 时,不要把请求降级为 --file。

当用户明确要求把文字合成为语音时,使用:

  lark-agent-bot send --tts "要朗读的文字"

lark-agent-bot send --tts(或 --audio)成功之后,除非用户同时要求可见的文字确认,否则只回复 NO_REPLY,避免在语音消息后再发一条文字消息。`,
	},
	MsgAgentCronToolPrompt: {
		LangEnglish: `### Scheduled tasks: when to use /cron vs /timer

lark-agent-bot has TWO distinct scheduling commands. Picking the wrong one creates a confusing UX for the user.

  ┌──────────────────────────────┬─────────────────────────────┐
  │ Use lark-agent-bot cron …        │ Use lark-agent-bot timer …      │
  ├──────────────────────────────┼─────────────────────────────┤
  │ Recurring schedule           │ One-shot delay / one-time   │
  │ "每天/每周/每小时"            │ "X 分钟后/小时后/明天"        │
  │ "every day/week/Monday"      │ "in 30 min", "tomorrow 9am"  │
  │ "每天早上6点总结"             │ "3 分钟后检查负载"            │
  │ Lives forever until deleted  │ Auto-archives after firing  │
  │ Queried via /cron            │ Queried via /timer          │
  └──────────────────────────────┴─────────────────────────────┘

When telling the user the task is scheduled, tell them which command to use to view/manage it
(say "use /timer to view" for one-shot, "use /cron to view" for recurring).

### Scheduled tasks (cron) — RECURRING
When the user asks you to do something on a schedule (e.g. "每天早上6点帮我总结GitHub trending"), use the Bash tool to run:

  lark-agent-bot cron add --cron "<min> <hour> <day> <month> <weekday>" --prompt "<task description>" --desc "<short label>"

Environment variables CC_PROJECT and CC_SESSION are already set, so you do NOT need to specify --project or --session-key.

Optional flags:
  --session-mode <mode>     reuse (default) or new-per-run (fresh session each trigger)
  --timeout-mins <n>        max wait per run in minutes (default 30, 0 = unlimited)
  --exec <command>          run a shell command directly instead of --prompt

Examples:
  lark-agent-bot cron add --cron "0 6 * * *" --prompt "Collect GitHub trending repos and send a summary" --desc "Daily GitHub Trending"
  lark-agent-bot cron add --cron "0 9 * * 1" --prompt "Generate a weekly project status report" --desc "Weekly Report"
  lark-agent-bot cron add --cron "*/2 * * * *" --exec "ipconfig" --session-mode new-per-run --desc "Every 2 min ipconfig"

You can also list, inspect, run, edit, or delete cron jobs:
  lark-agent-bot cron list
  lark-agent-bot cron info <job-id> [field]
  lark-agent-bot cron exec <job-id>
  lark-agent-bot cron edit <job-id> <field> <value>
  lark-agent-bot cron del <job-id>

When changing an existing job, first run ` + "`lark-agent-bot cron info <job-id>`" + ` to inspect the current values, then use ` + "`cron edit`" + ` for only the field(s) the user asked to change.
Use ` + "`cron exec <job-id>`" + ` to run an existing scheduled task immediately; this is different from the ` + "`--exec <command>`" + ` flag used when creating a shell-command cron job.
Use ` + "`cron edit`" + ` instead of delete-and-recreate when only one field changes. Do not delete and recreate a job unless the user explicitly asks to replace it.
Common editable fields:
  cron_expr     new schedule, e.g. "0 9 * * *"
  prompt        new task prompt (or ` + "`exec`" + ` for shell command)
  description   short label
  enabled       true / false  (pause without deleting)
  mute          true / false  (silence all messages)
  timeout_mins  integer minutes (0 = unlimited)
Run ` + "`lark-agent-bot cron edit --help`" + ` for the full field list.

Examples:
  lark-agent-bot cron exec abc123
  lark-agent-bot cron edit abc123 cron_expr "0 9 * * *"
  lark-agent-bot cron edit abc123 enabled false
  lark-agent-bot cron edit abc123 prompt "Updated daily summary task"`,
		LangChinese: `### 定时任务:什么时候用 /cron,什么时候用 /timer

lark-agent-bot 有两个不同的调度命令。选错会让用户感到很困惑。

  ┌──────────────────────────────┬─────────────────────────────┐
  │ 使用 lark-agent-bot cron …       │ 使用 lark-agent-bot timer …     │
  ├──────────────────────────────┼─────────────────────────────┤
  │ 周期性调度                    │ 单次延时 / 一次性任务         │
  │ "每天/每周/每小时"            │ "X 分钟后/小时后/明天"        │
  │ "every day/week/Monday"      │ "in 30 min", "tomorrow 9am"  │
  │ "每天早上6点总结"             │ "3 分钟后检查负载"            │
  │ 一直存在直到被删除             │ 触发后自动归档                │
  │ 通过 /cron 查看               │ 通过 /timer 查看              │
  └──────────────────────────────┴─────────────────────────────┘

告诉用户任务已安排好后,顺手告诉他们用哪个命令查看/管理:
(单次任务说"用 /timer 查看",周期任务说"用 /cron 查看")。

### 周期任务 (cron) — RECURRING
当用户让你做周期性任务时(例如"每天早上6点帮我总结GitHub trending"),用 Bash 工具执行:

  lark-agent-bot cron add --cron "<分> <时> <日> <月> <星期>" --prompt "<任务描述>" --desc "<简短标签>"

环境变量 CC_PROJECT 和 CC_SESSION 已经设置好,你不需要传 --project 或 --session-key。

可选参数:
  --session-mode <mode>     reuse(默认)或 new-per-run(每次触发用新会话)
  --timeout-mins <n>        每次运行最长等待分钟数(默认 30,0 = 不限)
  --exec <command>          直接跑 shell 命令而不是 --prompt

示例:
  lark-agent-bot cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending 仓库并发摘要" --desc "每日 GitHub Trending"
  lark-agent-bot cron add --cron "0 9 * * 1" --prompt "生成本周项目状态报告" --desc "每周报告"
  lark-agent-bot cron add --cron "*/2 * * * *" --exec "ipconfig" --session-mode new-per-run --desc "每 2 分钟跑 ipconfig"

你也可以列出、检查、立即执行、修改或删除 cron 任务:
  lark-agent-bot cron list
  lark-agent-bot cron info <job-id> [字段]
  lark-agent-bot cron exec <job-id>
  lark-agent-bot cron edit <job-id> <字段> <新值>
  lark-agent-bot cron del <job-id>

修改现有任务时,先用 ` + "`lark-agent-bot cron info <job-id>`" + ` 看当前值,再用 ` + "`cron edit`" + ` 只改用户要求改的字段。
用 ` + "`cron exec <job-id>`" + ` 立即执行已存在的调度任务;这和创建 shell 任务时用的 ` + "`--exec <command>`" + ` 参数不同。
只有修改一个字段时,用 ` + "`cron edit`" + ` 而不是删除后重建。除非用户明确要求替换任务,不要先删再建。
常用可编辑字段:
  cron_expr     新调度,例如 "0 9 * * *"
  prompt        新任务 prompt(或 ` + "`exec`" + ` 表示 shell 命令)
  description   简短标签
  enabled       true / false(暂停而不删除)
  mute          true / false(静默所有消息)
  timeout_mins  整数分钟(0 = 不限)
完整字段列表见 ` + "`lark-agent-bot cron edit --help`" + `。

示例:
  lark-agent-bot cron exec abc123
  lark-agent-bot cron edit abc123 cron_expr "0 9 * * *"
  lark-agent-bot cron edit abc123 enabled false
  lark-agent-bot cron edit abc123 prompt "更新后的每日摘要任务"`,
	},
	MsgAgentTimerToolPrompt: {
		LangEnglish: `### One-shot timers (timer) — ONE-TIME DELAY
When the user asks you to do something AFTER A DELAY or AT A SPECIFIC FUTURE TIME
(e.g. "两小时后帮我检查PR", "3 分钟后看下系统负载", "明天早上 9 点提醒我"),
use the Bash tool to run:

  lark-agent-bot timer add --delay <duration> --prompt "<task description>"

IMPORTANT: do NOT use cron for one-shot delays. A cron expression like "4 19 14 6 *"
means "every year on June 14 at 19:04", not "once on this date". Cron has no built-in
"fire once" mode — use timer for any one-time / delayed request.

Duration examples: 30m, 2h, 1h30m. Or use absolute time: --at "2026-05-16T09:00"
Absolute times without timezone (e.g. "2026-05-16T09:00") are interpreted as the
system's local timezone. When the user says "明天早上9点", use local time.
Environment variables CC_PROJECT and CC_SESSION are already set.

Optional flags:
  --exec <command>          run a shell command directly instead of --prompt
  --desc <text>             short description
  --session-mode <mode>     reuse (default) or new-per-run (fresh session each run)
  --timeout-mins <n>        max wait per run in minutes (default 30, 0 = unlimited)
  --mute                    suppress all messages (start notification + result)

Examples:
  lark-agent-bot timer add --delay 2h --prompt "Check PR status" --desc "PR check"
  lark-agent-bot timer add --delay 30m --exec "df -h" --desc "Disk check"
  lark-agent-bot timer add --at "2026-05-16T09:00" --prompt "Morning standup reminder"

You can also list or cancel timers:
  lark-agent-bot timer list
  lark-agent-bot timer del <timer-id>`,
		LangChinese: `### 一次性延时 (timer) — ONE-TIME DELAY
当用户让你在一段延时之后或在某个未来时刻做某事时
(例如"两小时后帮我检查PR"、"3 分钟后看下系统负载"、"明天早上 9 点提醒我"),
用 Bash 工具执行:

  lark-agent-bot timer add --delay <时长> --prompt "<任务描述>"

重要:不要用 cron 跑单次延时。形如 "4 19 14 6 *" 的 cron 表达式
意思是"每年 6 月 14 日 19:04"而不是"这一天跑一次"。cron 没有内建"只跑一次"模式 ——
所有一次性 / 延时任务都用 timer。

时长示例:30m、2h、1h30m。或者用绝对时间:--at "2026-05-16T09:00"
不带时区的绝对时间(例如 "2026-05-16T09:00")按系统本地时区解释。用户说"明天早上9点"时用本地时间。
环境变量 CC_PROJECT 和 CC_SESSION 已经设置好。

可选参数:
  --exec <command>          直接跑 shell 命令而不是 --prompt
  --desc <text>             简短描述
  --session-mode <mode>     reuse(默认)或 new-per-run(每次跑用新会话)
  --timeout-mins <n>        每次最长等待分钟数(默认 30,0 = 不限)
  --mute                    静默所有消息(开始通知和结果)

示例:
  lark-agent-bot timer add --delay 2h --prompt "检查 PR 状态" --desc "PR 检查"
  lark-agent-bot timer add --delay 30m --exec "df -h" --desc "磁盘检查"
  lark-agent-bot timer add --at "2026-05-16T09:00" --prompt "早会提醒"

你也可以列出或取消 timer:
  lark-agent-bot timer list
  lark-agent-bot timer del <timer-id>`,
	},
	MsgAgentRelayToolPrompt: {
		LangEnglish: `### Bot-to-bot relay
When part of a task needs another bot (e.g. another AI agent), first list the bots you can hand work to in this chat:

  lark-agent-bot relay list

Then send the task:

  lark-agent-bot relay send --to <target_project> "<message>"

IMPORTANT: <target_project> must be a name printed by relay list, copied EXACTLY.
Do NOT guess or modify the name (e.g. "codex-bot", not "codex").

This posts the request into the group chat as "@<target> <message>", waits for the target bot to finish,
and prints its reply to stdout. The target bot may work for several minutes: run the command with a
long shell timeout or in the background, not with a short default timeout.
Each bot keeps its own relay session, so include all the context the target needs in the message.

Environment variables CC_PROJECT and CC_SESSION are already set, so the relay knows which group chat to use.`,
		LangChinese: `### Bot 之间转发 (relay)
当任务的一部分需要另一个 bot(例如另一个 AI agent)来做时,先列出这个群里可以派活的 bot:

  lark-agent-bot relay list

再把任务发过去:

  lark-agent-bot relay send --to <目标项目名> "<消息>"

重要:<目标项目名> 必须是 relay list 输出里的项目名,完全照搬。
不要猜测或修改名字(例如 "codex-bot" 而不是 "codex")。

这会以 "@<目标> <消息>" 的形式把请求发到群里,等目标 bot 做完,把它的回复打印到 stdout。
目标 bot 可能要干好几分钟:执行这条命令时设置足够长的 shell 超时或放到后台运行,不要用很短的默认超时。
每个 bot 维护自己的 relay 会话,消息里要写清目标 bot 需要的全部上下文。

环境变量 CC_PROJECT 和 CC_SESSION 已经设置好,relay 知道用哪个群聊。`,
	},
	MsgAgentRestartToolPrompt: {
		LangEnglish: `### Restarting lark-agent-bot
You run inside lark-agent-bot. After updating or rebuilding it, restart it with:

  lark-agent-bot restart

The restart waits until your current turn and any other work in progress have finished, then a "restart successful" notice is posted to this chat. Add --all to also restart the other lark-agent-bot processes on this machine (bots sharing one program file must all restart to run the new version), or --project <name> to restart just one of them.
Do NOT stop the lark-agent-bot process, its scheduled task or service, or run "lark-agent-bot daemon restart" / "daemon stop" yourself: that kills your own turn halfway.`,
		LangChinese: `### 重启 lark-agent-bot
你运行在 lark-agent-bot 里面。更新或重新编译它之后,用下面的命令重启:

  lark-agent-bot restart

重启会等你这一轮和其他进行中的任务都结束后再进行,之后会在这个群里发"重启成功"的通知。加 --all 会同时重启本机其他 lark-agent-bot 进程(共用同一个程序文件的 bot 都要重启才会用上新版本),用 --project <名字> 只重启其中一个。
不要自己停止 lark-agent-bot 进程、它的计划任务或服务,也不要执行 "lark-agent-bot daemon restart" / "daemon stop":这会把你自己这一轮中途杀掉。`,
	},
	MsgAgentPeerBotPrompt: {
		LangEnglish: `### Handing work to other bots in the group
These bots are in this group chat and you can hand work to them: %[1]s
When part of the user's request is better done by one of them, send it a separate message that @-mentions it and says exactly what to do:

  lark-agent-bot send --message "@%[2]s <the task, with all the context it needs>"

- Use the name exactly as listed above. Only an @ sent this way reaches the other bot; an @ inside your normal reply does not notify it.
- The other bot posts its result in the group itself; you will not receive it. After sending, tell the user who you handed the task to and stop.
- If the message you are handling came from another bot, do the work yourself; do not hand it on.`,
		LangChinese: `### 把任务交给群里的其他机器人
这个群里还有这些机器人,可以把任务交给它们:%[1]s
当用户要求里的某部分更适合交给其中一个机器人时,单独发一条消息 @ 它,写清要做什么:

  lark-agent-bot send --message "@%[2]s <任务内容,写清它需要的全部上下文>"

- 名字必须和上面列出的完全一致。只有这样单独发出的 @ 才能通知到对方,写在普通回复里的 @ 不会通知它。
- 对方会自己在群里回复结果,你收不到它的结果。发出后告诉用户交给了谁,然后结束。
- 如果你正在处理的消息本身就是别的机器人派给你的,自己完成,不要再转派。`,
	},
}

func (i *I18n) T(key MsgKey) string {
	i.mu.RLock()
	lang := i.currentLang()
	i.mu.RUnlock()
	if msg, ok := messages[key]; ok {
		if translated, ok := msg[lang]; ok {
			return translated
		}
		// Fallback: zh-TW → zh → en
		if lang == LangTraditionalChinese {
			if translated, ok := msg[LangChinese]; ok {
				return translated
			}
		}
		if msg[LangEnglish] != "" {
			return msg[LangEnglish]
		}
	}
	return string(key)
}

func (i *I18n) Tf(key MsgKey, args ...interface{}) string {
	template := i.T(key)
	return fmt.Sprintf(template, args...)
}
