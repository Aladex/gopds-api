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
	MethodTagMatch       DetectionMethod = "tag_match"
	MethodOpenAI         DetectionMethod = "openai"
	MethodTagFallback    DetectionMethod = "tag_fallback"
	MethodLinguaFallback DetectionMethod = "lingua_fallback"
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

	// Caches for performance
	standardizationCache map[string]string
	textHashCache        map[string]LanguageDetectionResult
	cacheMutex           sync.RWMutex
}

// NewLanguageDetector creates a new language detector with lingua-go
func NewLanguageDetector(enableOpenAI bool, openaiTimeout time.Duration) *LanguageDetector {
	// Build lingua detector with all 75 supported languages
	detector := lingua.NewLanguageDetectorBuilder().
		FromLanguages(lingua.AllLanguages()...).
		WithPreloadedLanguageModels().
		Build()

	return &LanguageDetector{
		detector:             detector,
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

// DetectLanguage performs language detection with a simplified 3-case logic:
// 1. Tag + lingua agree → MethodTagMatch
// 2. Disagreement or no tag → ask OpenAI (MethodOpenAI)
// 3. OpenAI unavailable → fallback to lingua (MethodLinguaFallback)
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
		if textSample != "" {
			textHash := ld.hashText(textSample)
			ld.cacheMutex.Lock()
			ld.textHashCache[textHash] = result
			ld.cacheMutex.Unlock()
		}
		return result
	}

	// Detect with lingua-go
	detectedLang, confidence := ld.detectWithLingua(textSample)

	var result LanguageDetectionResult

	// Case 1: Tag and lingua agree
	if standardizedTag != "" && standardizedTag == detectedLang {
		logging.Infof("Language detection: tag and lingua agree on '%s' (confidence: %.2f)", detectedLang, confidence)
		result = LanguageDetectionResult{
			Language:   detectedLang,
			Method:     MethodTagMatch,
			Confidence: confidence,
		}
	} else {
		// Case 2: Disagreement, no tag, or unknown — ask OpenAI as arbiter
		logging.Infof("Language detection: no agreement (tag='%s', lingua='%s', confidence=%.2f), trying OpenAI",
			standardizedTag, detectedLang, confidence)

		openaiLang := ld.detectWithOpenAI(textSample)
		if openaiLang != "" && openaiLang != "unknown" {
			result = LanguageDetectionResult{
				Language:   openaiLang,
				Method:     MethodOpenAI,
				Confidence: confidence,
			}
		} else {
			// Case 3: OpenAI unavailable or failed — fallback to lingua
			result = LanguageDetectionResult{
				Language:   detectedLang,
				Method:     MethodLinguaFallback,
				Confidence: confidence,
			}
		}
	}

	// Cache the result
	textHash := ld.hashText(textSample)
	ld.cacheMutex.Lock()
	ld.textHashCache[textHash] = result
	ld.cacheMutex.Unlock()

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
				"content": "Detect the language of the following text. Reply with a single ISO 639-1 two-letter code only, nothing else.\n\nTEXT:\n" + sample,
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
