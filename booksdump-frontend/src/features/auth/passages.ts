/**
 * The verse that drifts behind the screens a reader meets before signing in.
 *
 * All of it is Pushkin, and all of it has been in the public domain for the
 * better part of a century — he died in 1837, and Russian copyright runs
 * seventy years past an author's death. One hand across all three screens is
 * deliberate: three different poets would read as a quotation collection, one
 * poet reads as a voice the product has.
 *
 * Each passage is chosen for the moment it sits behind, not at random. That is
 * the whole difference between this and decoration: a reader who recognises the
 * lines finds them apt, and a reader who does not still sees a page of Russian
 * verse.
 *
 * Whole poems rather than a stanza apiece. A background that drifts needs
 * enough text to keep drifting — a single stanza would be over in seconds and
 * begin repeating, which is exactly what makes a loop look like a loop.
 */

export interface Passage {
    /** The verse itself, one line per line. */
    lines: string[];
    /** Named for whoever reads the source, never shown. */
    source: string;
}

/** The three moments a reader can arrive at while signed out. */
export type Moment = 'returning' | 'beginning' | 'lost';

/**
 * One moment in both languages.
 *
 * Not translations of each other, and deliberately so. A Russian reader gets
 * Pushkin and an English one gets a poem of their own tradition that is about
 * the same thing — the interface already speaks two languages, and a screen
 * that says "Welcome back" over Russian verse would be the same seam we spent
 * a day pulling out of the email.
 */
export type Bilingual = { ru: Passage; en: Passage };

/**
 * Signing in: coming back after a while away.
 *
 * The card on this screen says "welcome back" and offers to continue reading;
 * the poem is about seeing someone again after years apart.
 */
const RETURNING_RU: Passage = {
    source: 'А. С. Пушкин, «Я помню чудное мгновенье…» (1825)',
    lines: [
        'Я помню чудное мгновенье:',
        'Передо мной явилась ты,',
        'Как мимолётное виденье,',
        'Как гений чистой красоты.',
        '',
        'В томленьях грусти безнадежной,',
        'В тревогах шумной суеты,',
        'Звучал мне долго голос нежный',
        'И снились милые черты.',
        '',
        'Шли годы. Бурь порыв мятежный',
        'Рассеял прежние мечты,',
        'И я забыл твой голос нежный,',
        'Твои небесные черты.',
        '',
        'В глуши, во мраке заточенья',
        'Тянулись тихо дни мои',
        'Без божества, без вдохновенья,',
        'Без слёз, без жизни, без любви.',
        '',
        'Душе настало пробужденье:',
        'И вот опять явилась ты,',
        'Как мимолётное виденье,',
        'Как гений чистой красоты.',
        '',
        'И сердце бьётся в упоенье,',
        'И для него воскресли вновь',
        'И божество, и вдохновенье,',
        'И жизнь, и слёзы, и любовь.',
    ],
};

/**
 * Registering: the beginning of a story.
 *
 * Every Russian child learns this one first, and it is literally the doorway
 * into a tale — which is what the reader is standing in.
 */
const BEGINNING_RU: Passage = {
    source: 'А. С. Пушкин, пролог к поэме «Руслан и Людмила» (1820)',
    lines: [
        'У лукоморья дуб зелёный;',
        'Златая цепь на дубе том:',
        'И днём и ночью кот учёный',
        'Всё ходит по цепи кругом;',
        'Идёт направо — песнь заводит,',
        'Налево — сказку говорит.',
        '',
        'Там чудеса: там леший бродит,',
        'Русалка на ветвях сидит;',
        'Там на неведомых дорожках',
        'Следы невиданных зверей;',
        'Избушка там на курьих ножках',
        'Стоит без окон, без дверей;',
        '',
        'Там лес и дол видений полны;',
        'Там о заре прихлынут волны',
        'На брег песчаный и пустой,',
        'И тридцать витязей прекрасных',
        'Чредой из вод выходят ясных,',
        'И с ними дядька их морской;',
        '',
        'Там королевич мимоходом',
        'Пленяет грозного царя;',
        'Там в облаках перед народом',
        'Через леса, через моря',
        'Колдун несёт богатыря;',
        '',
        'И там я был, и мёд я пил;',
        'У моря видел дуб зелёный;',
        'Под ним сидел, и кот учёный',
        'Свои мне сказки говорил.',
    ],
};

/**
 * Whatever went wrong: losing the way.
 *
 * "Сбились мы" — we have lost our road, and something is leading us in
 * circles. For a page that exists because an address led nowhere, there is no
 * better four words in the language.
 */
const LOST_RU: Passage = {
    source: 'А. С. Пушкин, «Бесы» (1830)',
    lines: [
        'Мчатся тучи, вьются тучи;',
        'Невидимкою луна',
        'Освещает снег летучий;',
        'Мутно небо, ночь мутна.',
        'Еду, еду в чистом поле;',
        'Колокольчик дин-дин-дин…',
        'Страшно, страшно поневоле',
        'Средь неведомых равнин!',
        '',
        '«Эй, пошёл, ямщик!…» — «Нет мочи:',
        'Коням, барин, тяжело;',
        'Вьюга мне слипает очи;',
        'Все дороги занесло;',
        'Хоть убей, следа не видно;',
        'Сбились мы. Что делать нам!',
        'В поле бес нас водит, видно,',
        'Да кружит по сторонам.',
        '',
        'Посмотри: вон, вон играет,',
        'Дует, плюет на меня;',
        'Вон — теперь в овраг толкает',
        'Одичалого коня;',
        'Там верстою небывалой',
        'Он торчал передо мной;',
        'Там сверкнул он искрой малой',
        'И пропал во тьме пустой».',
        '',
        'Мчатся тучи, вьются тучи;',
        'Невидимкою луна',
        'Освещает снег летучий;',
        'Мутно небо, ночь мутна.',
    ],
};

/**
 * The same three moments in English, taken from Project Gutenberg rather than
 * from memory: a misquoted classic on a public page is worse than no classic.
 */

/** Signing in: memory, and what returning restores. */
const RETURNING_EN: Passage = {
    source: 'William Shakespeare, Sonnet 30 (1609)',
    lines: [
        'When to the sessions of sweet silent thought',
        'I summon up remembrance of things past,',
        'I sigh the lack of many a thing I sought,',
        'And with old woes new wail my dear time\u2019s waste:',
        'Then can I drown an eye, unused to flow,',
        'For precious friends hid in death\u2019s dateless night,',
        'And weep afresh love\u2019s long since cancell\u2019d woe,',
        'And moan the expense of many a vanish\u2019d sight:',
        'Then can I grieve at grievances foregone,',
        'And heavily from woe to woe tell o\u2019er',
        'The sad account of fore-bemoaned moan,',
        'Which I new pay as if not paid before.',
        'But if the while I think on thee, dear friend,',
        'All losses are restor\u2019d and sorrows end.',
    ],
};

/** Registering: an enchanted place, announced. The English Lukomorye. */
const BEGINNING_EN: Passage = {
    source: 'Samuel Taylor Coleridge, «Kubla Khan» (1816)',
    lines: [
        'In Xanadu did Kubla Khan',
        'A stately pleasure-dome decree:',
        'Where Alph, the sacred river, ran',
        'Through caverns measureless to man',
        'Down to a sunless sea.',
        'So twice five miles of fertile ground',
        'With walls and towers were girdled round:',
        'And there were gardens bright with sinuous rills,',
        'Where blossomed many an incense-bearing tree;',
        'And here were forests ancient as the hills,',
        'Enfolding sunny spots of greenery.',
        '',
        'But oh! that deep romantic chasm which slanted',
        'Down the green hill athwart a cedarn cover!',
        'A savage place! as holy and enchanted',
        'As e\u2019er beneath a waning moon was haunted',
        'By woman wailing for her demon-lover!',
        'And from this chasm, with ceaseless turmoil seething,',
        'As if this earth in fast thick pants were breathing,',
        'A mighty fountain momently was forced:',
        'Amid whose swift half-intermitted burst',
        'Huge fragments vaulted like rebounding hail,',
        'Or chaffy grain beneath the thresher\u2019s flail:',
        'And \u2019mid these dancing rocks at once and ever',
        'It flung up momently the sacred river.',
        'Five miles meandering with a mazy motion',
        'Through wood and dale the sacred river ran,',
        'Then reached the caverns measureless to man,',
        'And sank in tumult to a lifeless ocean:',
        'And \u2019mid this tumult Kubla heard from far',
        'Ancestral voices prophesying war!',
        '',
        'The shadow of the dome of pleasure',
        'Floated midway on the waves;',
        'Where was heard the mingled measure',
        'From the fountain and the caves.',
        'It was a miracle of rare device,',
        'A sunny pleasure-dome with caves of ice!',
    ],
};

/** Whatever went wrong: the straightforward pathway, lost. */
const LOST_EN: Passage = {
    source: 'Dante, «Inferno» I, translated by Henry Wadsworth Longfellow (1867)',
    lines: [
        'Midway upon the journey of our life',
        'I found myself within a forest dark,',
        'For the straightforward pathway had been lost.',
        '',
        'Ah me! how hard a thing it is to say',
        'What was this forest savage, rough, and stern,',
        'Which in the very thought renews the fear.',
        '',
        'So bitter is it, death is little more;',
        'But of the good to treat, which there I found,',
        'Speak will I of the other things I saw there.',
        '',
        'I cannot well repeat how there I entered,',
        'So full was I of slumber at the moment',
        'In which I had abandoned the true way.',
        '',
        'But after I had reached a mountain\u2019s foot,',
        'At that point where the valley terminated,',
        'Which had with consternation pierced my heart,',
        '',
        'Upward I looked, and I beheld its shoulders,',
        'Vested already with that planet\u2019s rays',
        'Which leadeth others right by every road.',
    ],
};

/** Every moment, in both languages. */
export const PASSAGES: Record<Moment, Bilingual> = {
    returning: { ru: RETURNING_RU, en: RETURNING_EN },
    beginning: { ru: BEGINNING_RU, en: BEGINNING_EN },
    lost: { ru: LOST_RU, en: LOST_EN },
};

/**
 * The verse for a moment, in the language being shown.
 *
 * Anything that is not Russian gets the English one, which is the same rule the
 * emails follow: two languages are what the interface offers, and English is
 * the safer of the two to be wrong about.
 */
export function passageFor(moment: Moment, language: string): Passage {
    const pair = PASSAGES[moment];
    return language.toLowerCase().startsWith('ru') ? pair.ru : pair.en;
}
