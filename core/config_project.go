package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// configPage names a page of the /config card. The display page is open to
// every user; the other pages change project settings and are for admins.
type configPage string

const (
	configPageDisplay configPage = ""
	configPageProject configPage = "project"
)

// configAdminPages are the pages only admins may open, in tab order.
var configAdminPages = []configPage{configPageProject}

// parseConfigPage maps a page name from /config <page> or a card action to
// its page. "display" names the default page.
func parseConfigPage(name string) (configPage, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "display":
		return configPageDisplay, true
	case string(configPageProject):
		return configPageProject, true
	}
	return "", false
}

// configProjectKeys are the /config items on the project page. They change
// project settings, so setting them needs admin_from.
var configProjectKeys = []string{
	"show_context_indicator", "show_workdir_indicator", "reply_footer", "inject_sender", "agent_type",
}

// configRestartAction restarts the bot from the project page so a saved
// agent type takes effect.
const configRestartAction = "restart"

// isConfigAdminInvocation reports whether /config args open an admin page or
// change a project setting. Reading a value with /config get stays open.
func isConfigAdminInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	first := strings.ToLower(args[0])
	if page, ok := parseConfigPage(first); ok && page != configPageDisplay {
		return true
	}
	if first == configRestartAction {
		return true
	}
	key := first
	if first == "set" && len(args) > 1 {
		key = strings.ToLower(args[1])
	}
	return slices.Contains(configProjectKeys, key)
}

// saveProjectSettings writes u to the config file through the saver set by
// SetProjectSettingsSaver, the path the web admin uses too.
func (e *Engine) saveProjectSettings(u ProjectSettingsUpdate) error {
	if e.projectSettingsSaver == nil {
		return nil
	}
	if err := e.projectSettingsSaver(u); err != nil {
		return fmt.Errorf("save project settings: %w", err)
	}
	return nil
}

// configProjectItems are the project page items. Each saves to the config
// file first and applies only when the save succeeds, so a failed save
// leaves the running bot and the file in agreement.
func (e *Engine) configProjectItems(boolChoices []configChoice) []configItem {
	boolItem := func(key, desc, descZh string, get func() bool, apply func(bool), field func(*ProjectSettingsUpdate) **bool) configItem {
		return configItem{
			key:     key,
			desc:    desc,
			descZh:  descZh,
			page:    configPageProject,
			choices: boolChoices,
			getFunc: func() string { return fmt.Sprintf("%t", get()) },
			setFunc: func(v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return fmt.Errorf("invalid boolean: %s", v)
				}
				var u ProjectSettingsUpdate
				*field(&u) = &b
				if err := e.saveProjectSettings(u); err != nil {
					return err
				}
				apply(b)
				return nil
			},
		}
	}

	agents := ListRegisteredAgents()
	slices.Sort(agents)
	agentChoices := make([]configChoice, 0, len(agents))
	for _, name := range agents {
		agentChoices = append(agentChoices, configChoice{value: name, label: name})
	}

	return []configItem{
		boolItem("show_context_indicator",
			"Reply footer shows the model, tokens and context usage (true/false)",
			"回复页脚显示模型、token 和上下文用量 (true/false)",
			func() bool { return e.showContextIndicator }, e.SetShowContextIndicator,
			func(u *ProjectSettingsUpdate) **bool { return &u.ShowContextIndicator }),
		boolItem("show_workdir_indicator",
			"Reply footer shows the working directory (true/false)",
			"回复页脚显示工作目录 (true/false)",
			func() bool { return e.showWorkdirIndicator }, e.SetShowWorkdirIndicator,
			func(u *ProjectSettingsUpdate) **bool { return &u.ShowWorkdirIndicator }),
		boolItem("reply_footer",
			"Show a footer under each reply; off hides both lines above (true/false)",
			"每条回复下面显示页脚；关闭后上面两项都不显示 (true/false)",
			func() bool { return e.replyFooterEnabled }, e.SetReplyFooterEnabled,
			func(u *ProjectSettingsUpdate) **bool { return &u.ReplyFooter }),
		boolItem("inject_sender",
			"Tell the agent who sent each message (platform and user ID) (true/false)",
			"告诉 agent 每条消息是谁发的（平台和用户 ID）(true/false)",
			func() bool { return e.injectSender }, e.SetInjectSender,
			func(u *ProjectSettingsUpdate) **bool { return &u.InjectSender }),
		{
			key:     "agent_type",
			desc:    "Agent this project runs. Takes effect after a restart; providers the new agent cannot use are removed",
			descZh:  "这个项目使用的 agent。重启后生效；新 agent 用不了的服务商会被移除",
			page:    configPageProject,
			choices: agentChoices,
			getFunc: e.configAgentType,
			setFunc: e.setConfigAgentType,
		},
	}
}

// configAgentType is the agent type the config file names: the one saved
// from the card and waiting for a restart, else the running one.
func (e *Engine) configAgentType() string {
	e.pendingAgentTypeMu.Lock()
	defer e.pendingAgentTypeMu.Unlock()
	if e.pendingAgentType != "" {
		return e.pendingAgentType
	}
	return e.agent.Name()
}

// pendingAgentTypeChange returns the agent type that takes effect at the
// next restart, or "" when the config names the running agent.
func (e *Engine) pendingAgentTypeChange() string {
	e.pendingAgentTypeMu.Lock()
	defer e.pendingAgentTypeMu.Unlock()
	return e.pendingAgentType
}

func (e *Engine) setConfigAgentType(v string) error {
	if !slices.Contains(ListRegisteredAgents(), v) {
		return fmt.Errorf("unknown agent type %q", v)
	}
	if v == e.configAgentType() {
		return nil
	}
	if err := e.saveProjectSettings(ProjectSettingsUpdate{AgentType: &v}); err != nil {
		return err
	}
	e.pendingAgentTypeMu.Lock()
	defer e.pendingAgentTypeMu.Unlock()
	if v == e.agent.Name() {
		e.pendingAgentType = ""
	} else {
		e.pendingAgentType = v
	}
	return nil
}

// configRestart restarts the bot for the project page. Like /upgrade it waits
// for work in progress, and the success notice goes to sessionKey's chat.
func (e *Engine) configRestart(sessionKey string) string {
	busy, maxWait, err := e.RequestRestart(sessionKey, false)
	if err != nil {
		return e.i18n.Tf(MsgError, err)
	}
	if busy > 0 {
		return e.i18n.Tf(MsgUpgradeRestartWaiting, busy, int(maxWait/time.Minute))
	}
	return e.i18n.T(MsgRestarting)
}

// configPageButtons returns the page switcher shown to admins, with the
// current page highlighted.
func (e *Engine) configPageButtons(current configPage) []CardButton {
	pages := append([]configPage{configPageDisplay}, configAdminPages...)
	buttons := make([]CardButton, 0, len(pages))
	for _, page := range pages {
		label, name := e.i18n.T(MsgConfigPageDisplay), "display"
		if page == configPageProject {
			label, name = e.i18n.T(MsgConfigPageProject), string(configPageProject)
		}
		if page == current {
			buttons = append(buttons, PrimaryBtn(label, "nav:/config "+name))
		} else {
			buttons = append(buttons, DefaultBtn(label, "nav:/config "+name))
		}
	}
	return buttons
}

// hasAdmin reports whether admin_from names anyone. When it is empty nobody
// can open the admin pages, including to set the first admin.
func (e *Engine) hasAdmin() bool {
	e.userRolesMu.RLock()
	defer e.userRolesMu.RUnlock()
	return e.adminFrom != ""
}
