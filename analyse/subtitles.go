package analyse

import (
	"regexp"
	"strings"
)

// Subtitles beside a video: "Quiet Harbor (1995).en.srt", "Quiet Harbor (1995).es-MX.hi.srt",
// "Quiet Harbor (1995).fr.forced.ass". What follows the video's name says the
// language and the flags.

// Subtitle is a subtitle file and what its name says of it.
type Subtitle struct {
	Path string `json:"path"`
	// Language is the language as a short tag, with a region when written
	// ("en", "es-MX", "pt-BR"); "" when the name doesn't say.
	Language string `json:"language,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	// HearingImpaired is for subtitles that describe sounds too ("sdh",
	// "cc", "hi" after a language).
	HearingImpaired bool `json:"hearingImpaired,omitempty"`
	Default         bool `json:"default,omitempty"`
}

// subtitleTags reads what follows a video's name in a subtitle's name
// (".es-MX.hi", ".MX.es", ".English.forced", "2_English").
func subtitleTags(tail string) Subtitle {
	var s Subtitle
	var region string
	var words []string
	for _, w := range strings.FieldsFunc(tail, func(r rune) bool { return strings.ContainsRune(". []()", r) }) {
		if regionTag.MatchString(w) {
			words = append(words, w) // "pt_BR"
		} else {
			words = append(words, strings.FieldsFunc(w, func(r rune) bool { return r == '_' })...)
		}
	}
	hiSeen := false
	for _, w := range words {
		lower := strings.ToLower(w)
		switch lower {
		case "forced", "foreign":
			s.Forced = true
			continue
		case "sdh", "cc":
			s.HearingImpaired = true
			continue
		case "default":
			s.Default = true
			continue
		case "hi":
			// "hi" is hearing impaired after a language, Hindi alone.
			hiSeen = true
			continue
		}
		if s.Language == "" {
			if lang, reg, ok := languageTag(w); ok {
				s.Language = lang
				if reg != "" {
					region = reg
				}
				continue
			}
		}
		// A country's code in capitals beside a language: "MX.es".
		if len(w) == 2 && w == strings.ToUpper(w) && isLetters(w) && region == "" {
			if _, _, ok := languageTag(w); !ok || s.Language != "" {
				region = w
			}
		}
	}
	if hiSeen {
		if s.Language == "" {
			s.Language = "hi"
		} else {
			s.HearingImpaired = true
		}
	}
	if s.Language != "" && region != "" {
		s.Language += "-" + region
	}
	return s
}

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if !(s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
			return false
		}
	}
	return s != ""
}

// regionTag is a language with its region or script: "es-MX", "pt_BR",
// "es-419", "zh-Hans".
var regionTag = regexp.MustCompile(`^([A-Za-z]{2,3})[-_]([A-Za-z]{2}|\d{3}|[A-Za-z]{4})$`)

// languageTag reads one word as a language: a code ("en", "eng", "fre"),
// a name ("English", "Français"), or a code with a region ("es-MX").
// It gives the two-letter code, and the region when written.
func languageTag(w string) (lang, region string, ok bool) {
	if m := regionTag.FindStringSubmatch(w); m != nil {
		if l, ok := languages[strings.ToLower(m[1])]; ok {
			r := m[2]
			if len(r) == 2 {
				r = strings.ToUpper(r)
			} else if len(r) == 4 {
				r = strings.ToUpper(r[:1]) + strings.ToLower(r[1:])
			}
			return l, r, true
		}
	}
	l, ok := languages[Key(w)]
	return l, "", ok
}

// languages are languages' codes and names, to their two-letter code
// (ISO 639-1). Each line: the code, the three-letter codes (ISO 639-2,
// bibliographic and terminological), then names in English and in the
// language itself.
var languages = func() map[string]string {
	m := map[string]string{}
	for _, line := range []string{
		"ar ara arabic", "bg bul bulgarian", "bn ben bengali", "bs bos bosnian", "ca cat catalan catala",
		"cs cze ces czech cestina", "cy wel cym welsh", "da dan danish dansk", "de ger deu german deutsch allemand",
		"el gre ell greek", "en eng english anglais", "es spa spanish espanol castellano espagnol", "et est estonian",
		"eu baq eus basque", "fa per fas persian farsi", "fi fin finnish suomi", "fil filipino tagalog",
		"fr fre fra french francais vf vff vfq", "ga gle irish", "gl glg galician", "he heb hebrew",
		"hr hrv croatian hrvatski", "hu hun hungarian magyar", "hy arm hye armenian", "id ind indonesian",
		"is ice isl icelandic", "it ita italian italiano italien", "ja jpn japanese", "ka geo kat georgian",
		"kk kaz kazakh", "km khm khmer", "ko kor korean", "lt lit lithuanian", "lv lav latvian", "mk mac mkd macedonian",
		"ml mal malayalam", "mn mon mongolian", "ms may msa malay", "mt mlt maltese", "nb nob norwegian bokmal norsk",
		"nl dut nld dutch nederlands", "nn nno nynorsk", "no nor", "pa pan punjabi", "pl pol polish polski",
		"pt por portuguese portugues", "ro rum ron romanian", "ru rus russian", "sk slo slk slovak",
		"sl slv slovenian", "sq alb sqi albanian", "sr srp serbian", "sv swe swedish svenska", "sw swa swahili",
		"ta tam tamil", "te tel telugu", "th tha thai", "tr tur turkish turkce", "uk ukr ukrainian",
		"ur urd urdu", "vi vie vietnamese", "zh chi zho chinese mandarin", "hi hin hindi",
	} {
		words := strings.Fields(line)
		for _, w := range words {
			m[w] = words[0]
		}
	}
	return m
}()
