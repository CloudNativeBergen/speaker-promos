// Package lang decides which language a talk's generated text is written in,
// and holds the wording for each.
//
// It is its own package because both the social copy and the cards need it: a
// Norwegian talk whose card joins its speakers with "and" is wrong in the same
// way an English hook over a Norwegian abstract is. Putting it in the copy
// package would have made the renderer depend on the renderer's opposite.
package lang

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Language is the language a draft post is written in.
//
// Only the copy this package generates is translated. The cards stay in
// English: their text is conference chrome — the date line, the slot, the
// format — and it is the same on every card, so mixing languages across a set
// of images would look like a mistake rather than a choice. A post is different
// because it quotes the abstract, and an English sentence introducing Norwegian
// prose is what this exists to fix.
type Language string

const (
	// English is the default for a talk whose abstract is not Norwegian.
	English Language = "en"
	// Norwegian is Bokmål. "nb" and "no" both resolve to this.
	Norwegian Language = "no"
	// Auto asks for detection from the talk's own text. It is the zero value,
	// so a Language nobody set means "the talk's own".
	Auto Language = ""
)

// ParseLanguage accepts the spellings a user might reasonably type.
func ParseLanguage(s string) (Language, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto", "detect":
		return Auto, nil
	case "en", "eng", "english":
		return English, nil
	case "no", "nb", "nor", "norsk", "norwegian", "bokmål", "bokmal":
		return Norwegian, nil
	default:
		return "", fmt.Errorf("unknown language %q (want auto, en or no)", s)
	}
}

// norwegianMarkers are function words that occur constantly in Norwegian and
// not at all in English.
//
// Deliberately excluded are the ones the two languages share or that are too
// short to be safe: "i", "en", "et", "av", "er", "de", "den", "om", "for",
// "men". Including "men" in particular would score English prose as Norwegian.
var norwegianMarkers = map[string]bool{
	"og": true, "på": true, "til": true, "med": true, "som": true, "ikke": true,
	"det": true, "vi": true, "du": true, "kan": true, "har": true, "skal": true,
	"eller": true, "fra": true, "hvordan": true, "hvorfor": true, "være": true,
	"gjøre": true, "bruke": true, "sammen": true, "noen": true, "alle": true,
	"mer": true, "mye": true, "når": true, "hva": true, "hvem": true, "seg": true,
	"sin": true, "våre": true, "vår": true, "deg": true, "meg": true, "blir": true,
	"ble": true, "over": true, "etter": true, "uten": true, "mellom": true,
	"også": true, "bare": true, "veldig": true, "helt": true, "selv": true,
}

// englishMarkers are the mirror image, for the cases where a text has few
// Norwegian function words but is plainly English.
var englishMarkers = map[string]bool{
	"the": true, "and": true, "with": true, "your": true, "this": true,
	"that": true, "from": true, "what": true, "why": true, "how": true,
	"we": true, "you": true, "will": true, "are": true, "can": true,
	"about": true, "into": true, "their": true, "which": true, "when": true,
	"they": true, "our": true, "been": true, "have": true, "does": true,
}

// Detect guesses the language of a talk from its title and abstract.
//
// The abstract dominates simply by being longer, which is what you want: a
// bilingual title like "Friheten i Koden: Digital Sovereignty for the Nordic
// Age" should follow the language the body is actually written in.
//
// Scoring compares distinctive function words in both directions rather than
// looking for æ, ø and å. Those are a strong signal when present but absent
// from plenty of Norwegian text, and present in English text quoting a
// Norwegian name — so they only break ties here.
func Detect(title, abstract string) Language {
	text := strings.ToLower(title + " " + abstract)
	var no, en int
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		if norwegianMarkers[word] {
			no++
		} else if englishMarkers[word] {
			en++
		}
	}
	switch {
	case no > en:
		return Norwegian
	case en > no:
		return English
	case strings.ContainsAny(text, "æøå"):
		return Norwegian
	default:
		return English
	}
}

// Resolve turns a possibly-Auto language into a concrete one for a talk.
//
// Anything other than English or Norwegian — Auto, or a value from somewhere
// unvalidated — resolves by detection. Falling back to a fixed language instead
// would risk putting an English frame around a Norwegian abstract, which is the
// thing this is here to prevent.
func (l Language) Resolve(title, abstract string) Language {
	if l == English || l == Norwegian {
		return l
	}
	return Detect(title, abstract)
}

// Phrases is the wording generated text needs, per language.
type Phrases struct {
	// Speaking* and Workshop* are the verb phrases in "<who> <verb> <event>".
	// Norwegian has no verb-number agreement, so one form covers both a single
	// speaker and several — unlike English, which needs is/are.
	SpeakingSingular string
	SpeakingPlural   string
	WorkshopSingular string
	WorkshopPlural   string
	// At introduces the conference name.
	At string
	// And joins the last two names in a list.
	And string
	// Anonymous stands in when no speaker is named.
	Anonymous string
	// QuoteOpen and QuoteClose wrap the talk title. Norwegian uses angle
	// quotation marks.
	QuoteOpen, QuoteClose string
	// Weekdays and Months are indexed from time.Weekday and time.Month-1.
	Weekdays []string
	Months   []string
	// Date renders a weekday-and-date line from those names.
	Date func(p Phrases, t time.Time) string
}

var table = map[Language]Phrases{
	English: {
		SpeakingSingular: "is speaking",
		SpeakingPlural:   "are speaking",
		WorkshopSingular: "is running a workshop",
		WorkshopPlural:   "are running a workshop",
		At:               "at",
		And:              "and",
		Anonymous:        "One of our speakers",
		QuoteOpen:        "“", QuoteClose: "”",
		Weekdays: []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
		Months: []string{"January", "February", "March", "April", "May", "June",
			"July", "August", "September", "October", "November", "December"},
		Date: func(p Phrases, t time.Time) string {
			return fmt.Sprintf("%s %d %s", p.Weekdays[int(t.Weekday())], t.Day(), p.Months[int(t.Month())-1])
		},
	},
	Norwegian: {
		SpeakingSingular: "holder foredrag",
		SpeakingPlural:   "holder foredrag",
		WorkshopSingular: "holder workshop",
		WorkshopPlural:   "holder workshop",
		At:               "på",
		And:              "og",
		Anonymous:        "En av foredragsholderne våre",
		QuoteOpen:        "«", QuoteClose: "»",
		Weekdays: []string{"søndag", "mandag", "tirsdag", "onsdag", "torsdag", "fredag", "lørdag"},
		Months: []string{"januar", "februar", "mars", "april", "mai", "juni",
			"juli", "august", "september", "oktober", "november", "desember"},
		Date: func(p Phrases, t time.Time) string {
			// Norwegian writes the day as an ordinal: "mandag 26. oktober".
			return fmt.Sprintf("%s %d. %s", p.Weekdays[int(t.Weekday())], t.Day(), p.Months[int(t.Month())-1])
		},
	},
}

// Words returns the phrase table for a language, falling back to English so a
// value from somewhere unvalidated cannot produce empty text.
func (l Language) Words() Phrases {
	if p, ok := table[l]; ok {
		return p
	}
	return table[English]
}
