package app

import (
	"fmt"
	"regexp"

	skillsadapter "pentgo/internal/adapters/skillfs"
	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
)

var nonemptySkillCatalogMarker = regexp.MustCompile(`\A<pentgo-skill-catalog digest="([a-f0-9]{64})">`)
var emptySkillCatalogMarker = regexp.MustCompile(`\A<pentgo-skill-catalog digest="">`)

// catalogDigestFromMessage recognizes only a host-format catalog marker at the
// beginning of a persisted system message. It deliberately does not parse the
// model-facing catalog prose.
func catalogDigestFromMessage(message agent.Message) (string, bool) {
	if message.Role != agent.RoleSystem {
		return "", false
	}
	if match := nonemptySkillCatalogMarker.FindStringSubmatch(message.Content); match != nil {
		return match[1], true
	}
	return "", emptySkillCatalogMarker.MatchString(message.Content)
}

// ensureSessionSkillCatalog appends catalog context only when the startup
// catalog differs from the latest catalog already persisted for this session.
func ensureSessionSkillCatalog(transcript *storage.TranscriptStore, registry *skillsadapter.Registry) error {
	if transcript == nil {
		return fmt.Errorf("session transcript is unavailable")
	}

	currentDigest := ""
	if registry != nil {
		currentDigest = registry.Digest()
	}
	priorDigest, found := "", false
	for _, message := range transcript.Messages() {
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
	return transcript.Append(agent.Message{Role: agent.RoleSystem, Content: content})
}
