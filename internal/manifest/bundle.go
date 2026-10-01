package manifest

import (
	"fmt"

	"github.com/vehagn/speaker-promos/internal/cnd"
)

// bundleHeader introduces a per-talk manifest written into an export folder.
const bundleHeader = `# Everything that produced the cards in this folder.
#
# The TalkInfo object is a record: the whole talk as the tool saw it. It is
# ignored when this file is loaded, so the file can be passed straight back
# with --manifest.
#
# The SpeakerOverride and TalkOverride objects below it are the editable part.
# Their values are the ones actually used: a correction where you made one,
# otherwise the guess the tool made. Change one and pass this file with
# --manifest to apply it.
#
#   name:   the CMS is where people typed their own name, so accents go
#           missing. Correcting it here does not rename this folder.
#   title:  sets the card's role line verbatim, for roles that do not fit
#           "<job> at <employer>". Employer still drives what the post says.
#   image:  a speaker with no photo renders a monogram. Point this at a URL or
#           at a file next to this manifest to give them one.
`

// ConferenceInfo records the event a bundle belongs to.
type ConferenceInfo struct {
	Title      string `yaml:"title"`
	StartDate  string `yaml:"startDate"`
	EndDate    string `yaml:"endDate"`
	City       string `yaml:"city,omitempty"`
	Country    string `yaml:"country,omitempty"`
	Domain     string `yaml:"domain,omitempty"`
	ProgramURL string `yaml:"programUrl,omitempty"`
}

// TalkDetail records the talk itself.
type TalkDetail struct {
	ID string `yaml:"id"`
	// Title is what the cards say, after any displayTitle override.
	Title string `yaml:"title"`
	// SubmittedTitle is the title as submitted, present only when an override
	// changed it — so the record shows both.
	SubmittedTitle string   `yaml:"submittedTitle,omitempty"`
	Day            int      `yaml:"day"`
	Date           string   `yaml:"date"`
	StartTime      string   `yaml:"startTime,omitempty"`
	EndTime        string   `yaml:"endTime,omitempty"`
	Track          string   `yaml:"track,omitempty"`
	Format         string   `yaml:"format,omitempty"`
	FormatLabel    string   `yaml:"formatLabel,omitempty"`
	Level          string   `yaml:"level,omitempty"`
	Topics         []string `yaml:"topics,omitempty"`
	Abstract       string   `yaml:"abstract,omitempty"`
}

// SpeakerInfo records one speaker as the cards and copy saw them.
type SpeakerInfo struct {
	Name string `yaml:"name"`
	// Key is the name of this speaker's SpeakerOverride.
	Key  string `yaml:"key"`
	Slug string `yaml:"slug,omitempty"`
	// SubmittedTitle is the website's free-text title the employer was guessed
	// from, kept so a wrong guess can be judged without opening the website.
	SubmittedTitle string `yaml:"submittedTitle,omitempty"`
	// Title is the role line the card printed.
	Title    string `yaml:"title,omitempty"`
	Employer string `yaml:"employer,omitempty"`
	Job      string `yaml:"job,omitempty"`
	// EmployerGuessed marks an employer derived from SubmittedTitle rather than
	// taken from an override.
	EmployerGuessed bool   `yaml:"employerGuessed,omitempty"`
	Image           string `yaml:"image,omitempty"`
	// HasPhoto is false when the card fell back to a monogram, which is the
	// case an `image:` override exists to fix.
	HasPhoto   bool      `yaml:"hasPhoto"`
	ProfileURL string    `yaml:"profileUrl,omitempty"`
	Links      cnd.Links `yaml:"links,omitempty"`
}

// TalkInfoSpec is the whole record for one bundle.
//
// Deliberately no generation timestamp: an export should be reproducible, so
// that re-running it produces an identical folder and a bundle committed to git
// does not churn. The file times carry that information anyway.
type TalkInfoSpec struct {
	Conference ConferenceInfo `yaml:"conference"`
	Talk       TalkDetail     `yaml:"talk"`
	Speakers   []SpeakerInfo  `yaml:"speakers"`
	// Cards lists the image files written beside this manifest.
	Cards []string `yaml:"cards,omitempty"`
	// Warnings is what the render reported: truncated text, or emoji that some
	// renderers drop.
	Warnings []string `yaml:"warnings,omitempty"`
}

// BundleSpeaker is one speaker's editable override in a bundle.
type BundleSpeaker struct {
	Key  string
	Spec SpeakerSpec
}

// EncodeBundle renders the manifest documents for one talk as YAML: the record,
// then each speaker's override, then the talk's own.
//
// Unlike Set.Save, the speaker overrides are written as the caller pre-filled
// them, guesses included. An export folder is a record of what produced its
// cards, and a manifest that omitted the guessed employer would not say what
// the card actually claimed — nor give you a line to edit.
//
// The talk's own document is emitted only when it carries something: a display
// title, a language or a hidden flag. There is nothing to pre-fill it with
// otherwise, and an empty object would just be noise.
func (s *Set) EncodeBundle(info TalkInfoSpec, speakers []BundleSpeaker) ([]byte, error) {
	id := info.Talk.ID
	docs := []document{{APIVersion, KindTalkInfo, Metadata{id}, info}}
	for _, sp := range speakers {
		docs = append(docs, document{APIVersion, KindSpeakerOverride, Metadata{sp.Key}, sp.Spec})
	}
	if spec, ok := s.Talk(id); ok && !spec.empty() {
		docs = append(docs, document{APIVersion, KindTalkOverride, Metadata{id}, spec})
	}

	data, err := encode(bundleHeader, docs)
	if err != nil {
		return nil, fmt.Errorf("encoding manifest for %q: %w", info.Talk.Title, err)
	}
	return data, nil
}
