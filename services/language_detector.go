package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pemistahl/lingua-go"
	"gopds-api/logging"
)

// DetectionMethod represents how the language was determined
type DetectionMethod string

const (
	MethodTagMatch        DetectionMethod = "tag_match"
	MethodLinguaConfident DetectionMethod = "lingua_confident"
	MethodLinguaMedium    DetectionMethod = "lingua_medium"
	MethodOpenAI          DetectionMethod = "openai"
	MethodTagFallback     DetectionMethod = "tag_fallback"
	MethodUnknown         DetectionMethod = "unknown"
	MethodDefault         DetectionMethod = "default_ru"
)

// LanguageDetectionResult contains the detected language and metadata
type LanguageDetectionResult struct {
	Language   string
	Method     DetectionMethod
	Confidence float64
}

// LanguageDetector orchestrates language detection with voting mechanism
type LanguageDetector struct {
	detector      lingua.LanguageDetector
	enableOpenAI  bool
	openaiTimeout time.Duration

	// Set of ISO 639-1 codes that lingua can detect
	linguaSupported map[string]bool

	// Caches for performance
	standardizationCache map[string]string
	textHashCache        map[string]LanguageDetectionResult
	cacheMutex           sync.RWMutex
}

// NewLanguageDetector creates a new language detector with lingua-go
func NewLanguageDetector(enableOpenAI bool, openaiTimeout time.Duration) *LanguageDetector {
	// Build lingua detector with all 75 supported languages
	allLangs := lingua.AllLanguages()
	detector := lingua.NewLanguageDetectorBuilder().
		FromLanguages(allLangs...).
		WithPreloadedLanguageModels().
		Build()

	// Build lookup of ISO codes lingua can detect
	supported := make(map[string]bool, len(allLangs))
	for _, lang := range allLangs {
		code := strings.ToLower(lang.IsoCode639_1().String())
		supported[code] = true
	}

	return &LanguageDetector{
		detector:             detector,
		linguaSupported:      supported,
		enableOpenAI:         enableOpenAI,
		openaiTimeout:        openaiTimeout,
		standardizationCache: make(map[string]string),
		textHashCache:        make(map[string]LanguageDetectionResult),
	}
}

// StandardizeLanguage converts various language codes/names to ISO 639-1 format
func (ld *LanguageDetector) StandardizeLanguage(rawLang string) string {
	if rawLang == "" {
		return ""
	}

	// Check cache first
	ld.cacheMutex.RLock()
	if cached, found := ld.standardizationCache[rawLang]; found {
		ld.cacheMutex.RUnlock()
		return cached
	}
	ld.cacheMutex.RUnlock()

	// Normalize to lowercase
	normalized := strings.ToLower(strings.TrimSpace(rawLang))

	// Language mapping table: ISO 639-1, ISO 639-2/3, and English names
	// Covers all 75 lingua-go languages plus common FB2 metadata variants
	langMap := map[string]string{
		// Afrikaans
		"af": "af", "afr": "af", "afrikaans": "af",
		// Albanian
		"sq": "sq", "alb": "sq", "sqi": "sq", "albanian": "sq",
		// Arabic
		"ar": "ar", "ara": "ar", "arabic": "ar",
		// Armenian
		"hy": "hy", "arm": "hy", "hye": "hy", "armenian": "hy",
		// Azerbaijani
		"az": "az", "aze": "az", "azerbaijani": "az",
		// Basque
		"eu": "eu", "baq": "eu", "eus": "eu", "basque": "eu",
		// Belarusian
		"be": "be", "bel": "be", "belarusian": "be",
		// Bengali
		"bn": "bn", "ben": "bn", "bengali": "bn",
		// Bosnian
		"bs": "bs", "bos": "bs", "bosnian": "bs",
		// Bulgarian
		"bg": "bg", "bul": "bg", "bulgarian": "bg",
		// Catalan
		"ca": "ca", "cat": "ca", "catalan": "ca",
		// Chinese
		"zh": "zh", "chi": "zh", "zho": "zh", "chinese": "zh",
		// Croatian
		"hr": "hr", "hrv": "hr", "croatian": "hr",
		// Czech
		"cs": "cs", "cze": "cs", "ces": "cs", "czech": "cs",
		// Danish
		"da": "da", "dan": "da", "danish": "da",
		// Dutch
		"nl": "nl", "dut": "nl", "nld": "nl", "dutch": "nl",
		// English
		"en": "en", "eng": "en", "english": "en",
		// Esperanto
		"eo": "eo", "epo": "eo", "esperanto": "eo",
		// Estonian
		"et": "et", "est": "et", "estonian": "et",
		// Finnish
		"fi": "fi", "fin": "fi", "finnish": "fi",
		// French
		"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr",
		// Georgian
		"ka": "ka", "geo": "ka", "kat": "ka", "georgian": "ka",
		// German
		"de": "de", "ger": "de", "deu": "de", "german": "de",
		// Greek
		"el": "el", "gre": "el", "ell": "el", "greek": "el",
		// Gujarati
		"gu": "gu", "guj": "gu", "gujarati": "gu",
		// Hebrew
		"he": "he", "heb": "he", "hebrew": "he",
		// Hindi
		"hi": "hi", "hin": "hi", "hindi": "hi",
		// Hungarian
		"hu": "hu", "hun": "hu", "hungarian": "hu",
		// Icelandic
		"is": "is", "ice": "is", "isl": "is", "icelandic": "is",
		// Indonesian
		"id": "id", "ind": "id", "indonesian": "id",
		// Irish
		"ga": "ga", "gle": "ga", "irish": "ga",
		// Italian
		"it": "it", "ita": "it", "italian": "it",
		// Japanese
		"ja": "ja", "jpn": "ja", "japanese": "ja",
		// Kazakh
		"kk": "kk", "kaz": "kk", "kazakh": "kk",
		// Korean
		"ko": "ko", "kor": "ko", "korean": "ko",
		// Latin
		"la": "la", "lat": "la", "latin": "la",
		// Latvian
		"lv": "lv", "lav": "lv", "latvian": "lv",
		// Lithuanian
		"lt": "lt", "lit": "lt", "lithuanian": "lt",
		// Macedonian
		"mk": "mk", "mac": "mk", "mkd": "mk", "macedonian": "mk",
		// Malay
		"ms": "ms", "may": "ms", "msa": "ms", "malay": "ms",
		// Maori
		"mi": "mi", "mao": "mi", "mri": "mi", "maori": "mi",
		// Marathi
		"mr": "mr", "mar": "mr", "marathi": "mr",
		// Mongolian
		"mn": "mn", "mon": "mn", "mongolian": "mn",
		// Norwegian Bokmal
		"nb": "nb", "nob": "nb",
		// Norwegian Nynorsk
		"nn": "nn", "nno": "nn",
		// Norwegian (generic → Bokmal)
		"no": "nb", "nor": "nb", "norwegian": "nb",
		// Persian
		"fa": "fa", "per": "fa", "fas": "fa", "persian": "fa",
		// Polish
		"pl": "pl", "pol": "pl", "polish": "pl",
		// Portuguese
		"pt": "pt", "por": "pt", "portuguese": "pt",
		// Punjabi
		"pa": "pa", "pan": "pa", "punjabi": "pa",
		// Romanian
		"ro": "ro", "rum": "ro", "ron": "ro", "romanian": "ro",
		// Russian
		"ru": "ru", "rus": "ru", "russian": "ru",
		// Serbian
		"sr": "sr", "srp": "sr", "serbian": "sr",
		// Slovak
		"sk": "sk", "slo": "sk", "slk": "sk", "slovak": "sk",
		// Slovene
		"sl": "sl", "slv": "sl", "slovene": "sl", "slovenian": "sl",
		// Somali
		"so": "so", "som": "so", "somali": "so",
		// Spanish
		"es": "es", "spa": "es", "spanish": "es",
		// Swahili
		"sw": "sw", "swa": "sw", "swahili": "sw",
		// Swedish
		"sv": "sv", "swe": "sv", "swedish": "sv",
		// Tagalog
		"tl": "tl", "tgl": "tl", "tagalog": "tl",
		// Tamil
		"ta": "ta", "tam": "ta", "tamil": "ta",
		// Telugu
		"te": "te", "tel": "te", "telugu": "te",
		// Thai
		"th": "th", "tha": "th", "thai": "th",
		// Turkish
		"tr": "tr", "tur": "tr", "turkish": "tr",
		// Ukrainian
		"uk": "uk", "ukr": "uk", "ukrainian": "uk",
		// Urdu
		"ur": "ur", "urd": "ur", "urdu": "ur",
		// Vietnamese
		"vi": "vi", "vie": "vi", "vietnamese": "vi",
		// Welsh
		"cy": "cy", "wel": "cy", "cym": "cy", "welsh": "cy",
	}

	result, found := langMap[normalized]
	if !found {
		// If not found in map, return the first 2 chars if it looks like a code
		if len(normalized) == 2 || len(normalized) == 3 {
			result = normalized[:2]
		} else {
			result = "unknown"
		}
	}

	// Cache the result
	ld.cacheMutex.Lock()
	ld.standardizationCache[rawLang] = result
	ld.cacheMutex.Unlock()

	return result
}

// canLinguaArbitrate returns true if lingua can reliably override the tag.
// True when there is no tag, or the tag's language is one of lingua's 75 supported languages.
func (ld *LanguageDetector) canLinguaArbitrate(standardizedTag string) bool {
	if standardizedTag == "" || standardizedTag == "unknown" {
		return true // no tag to contradict
	}
	return ld.linguaSupported[standardizedTag]
}

// detectWithLingua uses lingua-go to detect language from text
func (ld *LanguageDetector) detectWithLingua(text string) (string, float64) {
	if text == "" {
		return "unknown", 0.0
	}

	// Lingua-go detection with confidence
	confidenceValues := ld.detector.ComputeLanguageConfidenceValues(text)

	if len(confidenceValues) == 0 {
		logging.Warn("Lingua-go returned no confidence values")
		return "unknown", 0.0
	}

	// Get the top result
	topResult := confidenceValues[0]
	langCode := ld.languageToISO639(topResult.Language())
	confidence := topResult.Value()

	logging.Debugf("Lingua-go detected: %s (confidence: %.2f)", langCode, confidence)

	return langCode, confidence
}

// languageToISO639 converts lingua.Language to ISO 639-1 code
func (ld *LanguageDetector) languageToISO639(lang lingua.Language) string {
	isoCode := lang.IsoCode639_1().String()
	return strings.ToLower(isoCode)
}

// DetectLanguage performs intelligent language detection with voting
func (ld *LanguageDetector) DetectLanguage(tagLang, textSample string) LanguageDetectionResult {
	// Check text hash cache first
	if textSample != "" {
		textHash := ld.hashText(textSample)
		ld.cacheMutex.RLock()
		if cached, found := ld.textHashCache[textHash]; found {
			ld.cacheMutex.RUnlock()
			logging.Debugf("Using cached language detection: %s (%s)", cached.Language, cached.Method)
			return cached
		}
		ld.cacheMutex.RUnlock()
	}

	// Standardize tag language
	standardizedTag := ld.StandardizeLanguage(tagLang)

	// If no text sample, use tag as fallback
	if textSample == "" || len([]rune(textSample)) < 20 {
		logging.Debug("No text sample or too short, using tag as fallback")
		result := LanguageDetectionResult{
			Language:   standardizedTag,
			Method:     MethodTagFallback,
			Confidence: 0.0,
		}
		// Cache the result for consistency
		if textSample != "" {
			textHash := ld.hashText(textSample)
			ld.cacheMutex.Lock()
			ld.textHashCache[textHash] = result
			ld.cacheMutex.Unlock()
		}
		return ld.ensureLanguage(result, textSample)
	}

	// Detect with lingua-go
	detectedLang, confidence := ld.detectWithLingua(textSample)

	// Voting Logic
	var result LanguageDetectionResult

	// Case 1: Perfect agreement - tag matches detected language
	if standardizedTag != "" && standardizedTag == detectedLang && confidence > 0.85 {
		logging.Infof("Language detection: perfect agreement on '%s' (confidence: %.2f)", detectedLang, confidence)
		result = LanguageDetectionResult{
			Language:   detectedLang,
			Method:     MethodTagMatch,
			Confidence: confidence,
		}
	} else if confidence > 0.85 && ld.canLinguaArbitrate(standardizedTag) {
		// Case 2: Lingua-go is very confident AND can arbitrate (tag is empty or lingua knows the tag language)
		logging.Infof("Language detection: lingua-go confident on '%s' (confidence: %.2f, tag: %s)", detectedLang, confidence, standardizedTag)
		result = LanguageDetectionResult{
			Language:   detectedLang,
			Method:     MethodLinguaConfident,
			Confidence: confidence,
		}
	} else if standardizedTag != "" && standardizedTag != "unknown" && standardizedTag != detectedLang {
		// Case 3: Tag disagrees with lingua and lingua can't reliably arbitrate - use OpenAI or trust tag
		logging.Warnf("Language detection disagreement: tag='%s', detected='%s' (confidence: %.2f)", standardizedTag, detectedLang, confidence)

		openaiLang := ld.detectWithOpenAI(textSample)
		if openaiLang != "" && openaiLang != "unknown" {
			result = LanguageDetectionResult{
				Language:   openaiLang,
				Method:     MethodOpenAI,
				Confidence: confidence,
			}
		} else {
			// No OpenAI — trust the tag (lingua can't reliably arbitrate for this language)
			result = LanguageDetectionResult{
				Language:   standardizedTag,
				Method:     MethodTagFallback,
				Confidence: confidence,
			}
		}
	} else if standardizedTag != "" && standardizedTag != "unknown" {
		// Case 4: Low confidence or no detection - fallback to tag
		logging.Infof("Language detection: low confidence (%.2f), falling back to tag '%s'", confidence, standardizedTag)
		result = LanguageDetectionResult{
			Language:   standardizedTag,
			Method:     MethodTagFallback,
			Confidence: confidence,
		}
	} else {
		// Case 5: No tag and no good detection
		logging.Warn("Language detection: no tag and low confidence, trying OpenAI")
		openaiLang := ld.detectWithOpenAI(textSample)
		if openaiLang != "" && openaiLang != "unknown" {
			result = LanguageDetectionResult{
				Language:   openaiLang,
				Method:     MethodOpenAI,
				Confidence: confidence,
			}
		} else {
			result = LanguageDetectionResult{
				Language:   "unknown",
				Method:     MethodUnknown,
				Confidence: confidence,
			}
		}
	}

	// Cache the result
	textHash := ld.hashText(textSample)
	ld.cacheMutex.Lock()
	ld.textHashCache[textHash] = result
	ld.cacheMutex.Unlock()

	return ld.ensureLanguage(result, textSample)
}

func (ld *LanguageDetector) ensureLanguage(result LanguageDetectionResult, textSample string) LanguageDetectionResult {
	if (result.Language == "" || result.Language == "unknown") && strings.TrimSpace(textSample) != "" {
		logging.Warnf("Language detection fallback to default 'ru' (method=%s)", result.Method)
		result.Language = "ru"
		result.Method = MethodDefault
		result.Confidence = 0.0
	}
	return result
}

func (ld *LanguageDetector) detectWithOpenAI(textSample string) string {
	if !ld.enableOpenAI {
		return ""
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		logging.Warn("OPENAI_API_KEY is not set, skipping OpenAI language detection")
		return ""
	}

	sample := truncateRunes(textSample, 300)
	ctx, cancel := context.WithTimeout(context.Background(), ld.openaiTimeout)
	defer cancel()

	requestBody := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": "Detect the language of the following text and ответь строго ISO-639-1 кодом (ru, en, uk, fr, de, es, it, pt, pl, cs, zh, ja, ko). If unsure, answer ru.\n\nTEXT:\n" + sample,
			},
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		logging.Warnf("OpenAI language detection marshal error: %v", err)
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
	if err != nil {
		logging.Warnf("OpenAI language detection request error: %v", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: ld.openaiTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logging.Warnf("OpenAI language detection call error: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logging.Warnf("OpenAI language detection bad status: %d", resp.StatusCode)
		return ""
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logging.Warnf("OpenAI language detection decode error: %v", err)
		return ""
	}
	if len(response.Choices) == 0 {
		return ""
	}

	reply := strings.TrimSpace(strings.ToLower(response.Choices[0].Message.Content))
	reply = strings.Trim(reply, "\"'` \n\t")
	if len(reply) > 5 {
		reply = strings.Fields(reply)[0]
	}

	standardized := ld.StandardizeLanguage(reply)
	if standardized == "" {
		return ""
	}
	return standardized
}

func truncateRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max])
}

// hashText creates a SHA256 hash of the text for caching
func (ld *LanguageDetector) hashText(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:])
}

// ClearCache clears all caches (useful for testing)
func (ld *LanguageDetector) ClearCache() {
	ld.cacheMutex.Lock()
	defer ld.cacheMutex.Unlock()

	ld.standardizationCache = make(map[string]string)
	ld.textHashCache = make(map[string]LanguageDetectionResult)

	logging.Debug("Language detector caches cleared")
}
