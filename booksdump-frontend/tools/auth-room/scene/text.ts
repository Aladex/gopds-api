/**
 * What is printed on the paper in the reading room.
 *
 * The book on the table is a Russian book whatever language the interface
 * speaks: it is an object in the room, like the table, not a label. The
 * openings are public-domain classics (Tolstoy, Gogol, Dostoevsky, Chekhov,
 * Pushkin) and one plain paragraph of our own, cut to a few sentences — the
 * page repeats them until it is full, which at this size nobody can read
 * closely enough to notice.
 */

export interface Passage {
    /** Running head, set in italics at the top of every page. */
    title: string;
    /** A chapter heading on the recto, if the opening has one. */
    heading: string | null;
    body: string;
}

export const PASSAGES: readonly Passage[] = [
    {
        title: 'Анна Каренина',
        heading: 'Глава первая',
        body: 'Все счастливые семьи похожи друг на друга, каждая несчастливая семья несчастлива по-своему. Все смешалось в доме Облонских. Жена узнала, что муж был в связи с бывшею в их доме француженкою-гувернанткой, и объявила мужу, что не может жить с ним в одном доме.',
    },
    {
        title: 'Мёртвые души',
        heading: null,
        body: 'В ворота гостиницы губернского города NN въехала довольно красивая рессорная небольшая бричка, в какой ездят холостяки: отставные подполковники, штабс-капитаны, помещики, имеющие около сотни душ крестьян, — словом, все те, которых называют господами средней руки.',
    },
    {
        title: 'Преступление и наказание',
        heading: 'I',
        body: 'В начале июля, в чрезвычайно жаркое время, под вечер, один молодой человек вышел из своей каморки, которую нанимал от жильцов в С—м переулке, на улицу и медленно, как бы в нерешимости, отправился к К—ну мосту.',
    },
    {
        title: 'Дама с собачкой',
        heading: null,
        body: 'Говорили, что на набережной появилось новое лицо: дама с собачкой. Дмитрий Дмитрич Гуров, проживший в Ялте уже две недели и привыкший тут, тоже стал интересоваться новыми лицами.',
    },
    {
        title: 'Капитанская дочка',
        heading: null,
        body: 'Отец мой Андрей Петрович Гринев в молодости своей служил при графе Минихе и вышел в отставку премьер-майором. С тех пор жил он в своей Симбирской деревне, где и женился на девице Авдотье Васильевне Ю., дочери бедного тамошнего дворянина.',
    },
    {
        title: 'Вечер',
        heading: 'Часть вторая',
        body: 'Был тот час вечера, когда в окнах ещё держится свет, а на улице уже темно, и всякий прохожий кажется знакомым. Он шёл не спеша, перебирая в памяти страницы, прочитанные днём, и не мог вспомнить, чем кончилась глава.',
    },
];

/**
 * The golden post: Uisky's Law, the Leprosorium's most quoted observation,
 * printed on the one sheet that is worth an invite.
 */
export const GOLDEN_POST: Passage = {
    title: 'Лепрозорий',
    heading: 'Закон Уйского',
    body: 'Лепрозорий, всё-таки, удивительный блог. Даже если написать «Однажды я съел валенок, а потом три дня срал балалайками», обязательно прибежит двадцать человек, которые расскажут аналогичные случаи из своей жизни.',
};

/** The folio of a passage's recto; the verso is one more. */
export const folioOf = (index: number) => 17 + index * 2;
export const GOLDEN_FOLIO = 113;

export const PLATE = {
    runningHead: 'Иллюстрации',
    number: 'Табл. I',
    caption: 'Дэнни Де Вито — клёвый чувак.',
    subcaption: 'Все были бы рады его видеть. Но без инвайта не зайдёт и он.',
};

/** The lettering on the library stamp's ring, repeated once round. */
export const STAMP_RING = 'СУВЕРЕННЫЙ ЛЕПРОЗОРИЙ · БИБЛИОТЕКА · ';
