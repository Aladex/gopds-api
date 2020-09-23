package utils

import "bytes"

var dictionary = map[string]string{
	"А": "A",
	"а": "a",
	"Б": "B",
	"б": "b",
	"В": "V",
	"в": "v",
	"Г": "G",
	"г": "g",
	"Д": "D",
	"д": "d",
	"Е": "E",
	"е": "e",
	"Ё": "Jo",
	"ё": "jo",
	"Ж": "Zh",
	"ж": "zh",
	"З": "Z",
	"з": "z",
	"И": "I",
	"и": "i",
	"Й": "J",
	"й": "j",
	"К": "K",
	"к": "k",
	"Л": "L",
	"л": "l",
	"М": "M",
	"м": "m",
	"Н": "N",
	"н": "n",
	"О": "O",
	"о": "o",
	"П": "P",
	"п": "p",
	"Р": "R",
	"р": "r",
	"С": "S",
	"с": "s",
	"Т": "T",
	"т": "t",
	"У": "U",
	"у": "u",
	"Ф": "F",
	"ф": "f",
	"Х": "H",
	"х": "h",
	"Ц": "C",
	"ц": "c",
	"Ч": "Ch",
	"ч": "ch",
	"Ш": "Sh",
	"ш": "sh",
	"Щ": "Shh",
	"щ": "shh",
	"Ъ": "",
	"ъ": "",
	"Ы": "Y",
	"ы": "y",
	"Ь": "",
	"ь": "",
	"Э": "Je",
	"э": "je",
	"Ю": "Ju",
	"ю": "ju",
	"Я": "Ja",
	"я": "ja",
}

// Translit transliterate from russian to latin
func Translit(s string) string {
	var buffer bytes.Buffer

	for _, v := range s {
		if char, ok := dictionary[string(v)]; ok {
			buffer.WriteString(char)
		} else {
			buffer.WriteString(string(v))
		}
	}

	return buffer.String()
}
