package bots

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"minecraft-server/server"
)

// DefaultBadwordsPath is where NewNosleeperBot looks for the bad-words
// list. Same convention as banlist.json — kept at the repo root so it can
// be edited without a rebuild.
const DefaultBadwordsPath = "badwords.json"

// BotName is the display name used as the sender of moderator
// announcements (e.g. "<NoSleeperBot|Player> выдал mute игроку Player").
const BotName = "NoSleeperBot"

// Moderation thresholds. Messages with strictly more than CensorMax bad
// words trigger a mute of MuteDuration; messages with 1..CensorMax bad
// words get their text replaced via rewriteMessage; clean messages pass
// through unchanged.
const (
	CensorMax    = 1
	MuteDuration = 15 * time.Minute
)

type NosleeperBot struct {
	srv *server.Server

	// badwords holds the lowercased blacklist as a set for O(1) lookup
	// from isBadWord. Guarded by mu so LoadBadwords can replace it at
	// runtime (e.g. from a future /reloadbadwords command) while Check
	// is reading on another goroutine.
	mu       sync.RWMutex
	badwords map[string]struct{}
}

// NewNosleeperBot is the public constructor — main.go calls this instead of
// using a struct literal because srv is unexported and can't be set from
// outside the server package.
//
// Attempts to load DefaultBadwordsPath on construction; a missing file
// logs a warning and the bot starts with an empty list (every message
// passes).
func NewNosleeperBot(s *server.Server) *NosleeperBot {
	b := &NosleeperBot{srv: s, badwords: map[string]struct{}{}}
	if err := b.LoadBadwords(DefaultBadwordsPath); err != nil {
		slog.Warn("nosleeperbot: bad-words load failed",
			"path", DefaultBadwordsPath, "err", err)
	}
	return b
}

// LoadBadwords reads a JSON array of strings from path and replaces the
// in-memory blacklist atomically. Words are trimmed + lowercased so
// matching is case-insensitive without re-folding on every call.
//
// JSON format:
//
//	["spam", "scam", "грубое-слово"]
func (b *NosleeperBot) LoadBadwords(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	set := make(map[string]struct{}, len(list))
	for _, w := range list {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		set[w] = struct{}{}
	}
	b.mu.Lock()
	b.badwords = set
	b.mu.Unlock()
	slog.Info("nosleeperbot: bad-words loaded", "path", path, "count", len(set))
	return nil
}

func (b *NosleeperBot) Check(c *server.ClientConnection, message string) server.ChatVerdict {
	n := b.countBadwords(message)
	switch {
	case n == 0:
		return server.ChatVerdict{Allow: true}
	case n > CensorMax: // 4+ → жёсткий мьют
		b.announceMute(c)
		return server.ChatVerdict{
			Allow:   false,
			MuteFor: MuteDuration,
		}
	default: // 1..CensorMax → замена всего предложения
		return server.ChatVerdict{
			Allow:   true,
			Rewrite: b.rewriteMessage(message),
		}
	}
}

// countBadwords counts how many tokens in message hit the bad-words set.
// Tokens that repeat are counted each time (so "мат мат мат мат" = 4),
// matching the user-visible expectation that a 4-word spew of profanity
// is "more" than a single slip.
func (b *NosleeperBot) countBadwords(message string) int {
	n := 0
	for _, tok := range tokenize(message) {
		if b.isBadWord(tok) {
			n++
		}
	}
	return n
}

// shouldCensor is the policy predicate Check uses to decide whether to
// rewrite — kept as a thin wrapper so callers and tests can ask the
// question by name rather than counting.
func (b *NosleeperBot) shouldCensor(message string) bool {
	n := b.countBadwords(message)
	return n > 0 && n <= CensorMax
}

// shouldMute is the policy predicate Check uses to decide whether to
// drop + mute. Wrapper for symmetry with shouldCensor.
func (b *NosleeperBot) shouldMute(message string) bool {
	return b.countBadwords(message) > CensorMax
}

// rewriteMessage produces the broadcast text when shouldCensor==true.
// Today: just stars; later you might mask only the offending tokens.
func (b *NosleeperBot) rewriteMessage(message string) string {
	_ = message
	phrases := []string{
		"Свистать всех наверх!",
		"Pew pew pepepew!",
		"Тинкер в деле!",
		"Я гений, и это доказано.",
		"Ха! Слишком просто.",
		"Наука побеждает.",
		"Мои расчёты безупречны.",
		"Перезарядка завершена.",
		"Включаем мозги.",
		"Технологии решают всё.",
		"Это было предсказуемо.",
		"Не угнаться за прогрессом.",
		"Механизмы активированы.",
		"Ракеты готовы.",
		"Лазеры онлайн.",
		"Я всё просчитал.",
		"Отличный эксперимент.",
		"Хе-хе-хе!",
		"Бум! Наука!",
		"Осторожнее с гением.",
		"Мои машины не знают пощады.",
		"Немного инженерии.",
		"Слишком умён для вас.",
		"Система уничтожения активна.",
		"Потрясающий результат.",
		"Пора модернизировать вас.",
		"Это называется эффективность.",
		"Восхитительно!",
		"Техника превыше силы.",
		"Гениально. Как всегда.",
		"Уничтожение подтверждено.",
		"Я только разогреваюсь.",
		"Энергия на максимуме.",
		"Мозг против мускулов.",
		"Машинам нужен мастер.",
		"Научный подход.",
		"Мои формулы работают.",
		"Тинкер никогда не ошибается.",
		"Эксперимент удался.",
		"Мои ракеты любят тебя.",
		"Смотри и учись.",
		"Слишком медленно.",
		"Хочешь апгрейд?",
		"Технологическое превосходство.",
		"Наука требует жертв.",
		"Мой IQ зашкаливает.",
		"Точность — признак мастерства.",
		"Время для лазеров.",
		"Сейчас будет взрыв.",
		"Этого я и ожидал.",
		"Потрясающая работа, я знаю.",
	}

	return phrases[rand.Intn(len(phrases))]
}

// announceMute broadcasts a chat line into the offender's current
// instance so everyone present sees who was muted by whom. The line
// renders in chat as:
//
//	<NoSleeperBot|Spammer> выдал mute игроку Spammer
//
// (sender is "BotName|PlayerName"; message follows). Called from Check
// right before the mute-verdict is returned to the server.
func (b *NosleeperBot) announceMute(c *server.ClientConnection) {
	inst := c.Instance()
	if inst == nil {
		return // pre-login or already torn down — nothing to broadcast to
	}
	inst.BroadcastChat(BotName, "выдал mute игроку "+c.Name())
	c.SendChat(BotName, "Verdammter Idiot, mutterloses Monster, ich werde dich mit einem Stock ficken, du verdienst es nicht einmal, auf diesem Server zu atmen, verdammter Müll! Мут на "+MuteDuration.String()+" за нарушение правил чата.")
}

// isBadWord normalizes word the same way LoadBadwords does, then asks the
// blacklist set. Read-locks the bot so a concurrent LoadBadwords doesn't
// tear the map.
func (b *NosleeperBot) isBadWord(word string) bool {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return false
	}
	b.mu.RLock()
	_, ok := b.badwords[w]
	b.mu.RUnlock()
	return ok
}

// tokenize splits message into lower-cased word tokens. Anything that
// isn't a letter or digit (ASCII or beyond) counts as a separator, so
// "Hello, world!" → ["Hello","world"] before lower-casing in isBadWord.
func tokenize(message string) []string {
	out := []string{}
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range message {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r >= 128: // keep non-ASCII letters together (cyrillic, etc.)
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}
