package runtime

import (
	"fmt"
	"regexp"
	"strings"

	"pentgo/internal/core"
	sessionstate "pentgo/internal/project/session"
	skillsadapter "pentgo/internal/tools"
)

var skillCatalogMarker = regexp.MustCompile(`\A<pentgo-skill-catalog digest="([a-f0-9]{64})?">`)

// catalogDigestFromMessage recognizes only a host-format catalog marker at the
// beginning of a persisted system message. It deliberately does not parse the
// model-facing catalog prose.
func catalogDigestFromMessage(message core.Message) (string, bool) {
	if message.Role != core.RoleSystem {
		return "", false
	}
	match := skillCatalogMarker.FindStringSubmatch(message.Content)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// ensureSessionSkillCatalog appends catalog context only when the startup
// catalog differs from the latest catalog already persisted for this session.
func matchedSkillContext(registry *skillsadapter.Registry, request string) string {
	if registry == nil {
		return ""
	}
	skill, ok := registry.Match(request)
	if !ok {
		return ""
	}
	body, err := registry.Load(skill.Name)
	if err != nil || strings.TrimSpace(body) == "" {
		return ""
	}
	return "<pentgo-preloaded-skill name=\"" + skill.Name + "\">\n" +
		"The user request clearly matches this skill. Follow it before specialized work; do not call load_skill for this same skill again.\n" +
		body + "\n</pentgo-preloaded-skill>"
}

func ensureSessionSkillCatalog(conversation *sessionstate.ConversationStore, registry *skillsadapter.Registry) error {
	if conversation == nil {
		return fmt.Errorf("session conversation is unavailable")
	}

	currentDigest := ""
	if registry != nil {
		currentDigest = registry.Digest()
	}
	priorDigest, found := "", false
	for _, message := range conversation.Messages() {
		if digest, ok := catalogDigestFromMessage(message); ok {
			priorDigest, found = digest, true
		}
	}
	if found && priorDigest == currentDigest {
		return nil
	}
	if !found && currentDigest == "" {
		return nil
	}

	content := ""
	if registry == nil {
		content = "<pentgo-skill-catalog digest=\"\">\n" +
			"This catalog completely replaces every earlier PentGo skill catalog in this session.\n" +
			"No PentGo skills are currently available. Do not use names from earlier PentGo skill catalogs.\n" +
			"</pentgo-skill-catalog>"
	} else {
		content = registry.RenderCatalog(found)
	}
	if content == "" {
		return nil
	}
	return conversation.Append(core.Message{Role: core.RoleSystem, Content: content})
}
