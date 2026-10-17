package core

import "errors"

// A topic's workspace binding sits under its chat's: the platform sets
// Message.LegacyChannelKey to the chat-level scope, whose binding new topics
// start from (migrateLegacyWorkspaceBindings). Binding a project in a topic
// binds only the topic; the bind reply then offers to apply it to the whole
// group, which "/workspace group" and the card button do.

var (
	errWorkspaceNotInTopic   = errors.New("not in a topic")
	errWorkspaceTopicUnbound = errors.New("topic has no project")
)

// groupWorkspaceChannelKey returns the chat-level binding key of a message in
// a topic, or "" outside a topic.
func groupWorkspaceChannelKey(msg *Message) string {
	if msg == nil || msg.LegacyChannelKey == "" {
		return ""
	}
	key := workspaceChannelKey(msg.Platform, msg.LegacyChannelKey)
	if key == effectiveWorkspaceChannelKey(msg) {
		return ""
	}
	return key
}

// groupWorkspaceOffer returns the project bound to msg's topic when applying
// it to the whole group would change the group's project, else "".
func (e *Engine) groupWorkspaceOffer(msg *Message) string {
	groupKey := groupWorkspaceChannelKey(msg)
	if groupKey == "" || e.workspaceBindings == nil {
		return ""
	}
	projectKey := "project:" + e.name
	topic := e.workspaceBindings.Lookup(projectKey, effectiveWorkspaceChannelKey(msg))
	if topic == nil {
		return ""
	}
	if group := e.workspaceBindings.Lookup(projectKey, groupKey); group != nil && group.Workspace == topic.Workspace {
		return ""
	}
	return topic.Workspace
}

// appendGroupWorkspaceOffer asks whether workspace, just bound to a topic,
// should be the whole group's project too.
func (e *Engine) appendGroupWorkspaceOffer(cb *CardBuilder, workspace string) {
	cb.Markdown(e.i18n.Tf(MsgWsGroupOffer, workspace))
	cb.Buttons(
		PrimaryBtn(e.i18n.T(MsgWsGroupApply), "act:/workspace group"),
		DefaultBtn(e.i18n.T(MsgWsGroupTopicOnly), "nav:/workspace bind"),
	)
}

// offerGroupWorkspace follows a bind reply in a topic with that question.
func (e *Engine) offerGroupWorkspace(p Platform, msg *Message) {
	workspace := e.groupWorkspaceOffer(msg)
	if workspace == "" {
		return
	}
	if supportsCards(p) {
		cb := NewCard()
		e.appendGroupWorkspaceOffer(cb, workspace)
		e.replyWithCard(p, msg.ReplyCtx, cb.Build())
		return
	}
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsGroupOffer, workspace)+"\n"+e.i18n.T(MsgWsGroupTextHint))
}

// applyWorkspaceToGroup binds the project of msg's topic to the whole group,
// re-reading it rather than trusting a card. New topics start with it; topics
// that have a project of their own keep it.
func (e *Engine) applyWorkspaceToGroup(msg *Message) (string, error) {
	groupKey := groupWorkspaceChannelKey(msg)
	if groupKey == "" || e.workspaceBindings == nil {
		return "", errWorkspaceNotInTopic
	}
	projectKey := "project:" + e.name
	topic := e.workspaceBindings.Lookup(projectKey, effectiveWorkspaceChannelKey(msg))
	if topic == nil {
		return "", errWorkspaceTopicUnbound
	}
	channelName := ""
	if group := e.workspaceBindings.Lookup(projectKey, groupKey); group != nil {
		channelName = group.ChannelName
	}
	e.workspaceBindings.Bind(projectKey, groupKey, channelName, topic.Workspace)
	return topic.Workspace, nil
}

func (e *Engine) groupWorkspaceError(err error) string {
	if errors.Is(err, errWorkspaceTopicUnbound) {
		return e.i18n.T(MsgWsNoBinding)
	}
	return e.i18n.T(MsgWsGroupNotTopic)
}
