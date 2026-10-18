// API fixtures for the booksdump scenes: every /api call the frontend makes is
// answered here, so the real UI runs from `vite` dev with no backend at all.

export const SENDER_ADDRESS = '3f6c2a9e-8b41-4d7c-9a15-c2e07b5d1f84@booksdump.com';
export const KINDLE_ADDRESS = 'reader_k7q2xm@kindle.com';

const BOOKS = {
    ru: [
        {
            id: 101,
            title: 'Анна Каренина',
            author: 'Лев Толстой',
            series: 'Русская классика',
            genre: 'Классическая проза',
            annotation:
                'Роман о любви, долге и выборе, который каждый делает сам. Одна из главных книг мировой литературы.',
            palette: ['#7a2e2e', '#f3e6d0'],
        },
        {
            id: 102,
            title: 'Война и мир',
            author: 'Лев Толстой',
            series: 'Русская классика',
            genre: 'Классическая проза',
            annotation: 'Эпопея о русском обществе в эпоху войн против Наполеона.',
            palette: ['#23415a', '#e8edf2'],
        },
        {
            id: 103,
            title: 'Детство. Отрочество. Юность',
            author: 'Лев Толстой',
            series: '',
            genre: 'Классическая проза',
            annotation: 'Автобиографическая трилогия, с которой Толстой вошёл в литературу.',
            palette: ['#3d5a3a', '#eef1e6'],
        },
    ],
    en: [
        {
            id: 201,
            title: 'Pride and Prejudice',
            author: 'Jane Austen',
            series: 'Penguin Classics',
            genre: 'Classic fiction',
            annotation:
                'Elizabeth Bennet and Mr Darcy misjudge each other at first sight, and spend a novel finding out how.',
            palette: ['#5a2340', '#f4e8ee'],
        },
        {
            id: 202,
            title: 'Emma',
            author: 'Jane Austen',
            series: 'Penguin Classics',
            genre: 'Classic fiction',
            annotation: 'A matchmaker who is sure she knows best, until she does not.',
            palette: ['#2f4f6f', '#e7eef5'],
        },
        {
            id: 203,
            title: 'Persuasion',
            author: 'Jane Austen',
            series: '',
            genre: 'Classic fiction',
            annotation: 'A second chance at a love given up eight years earlier.',
            palette: ['#4a5a2f', '#eef2e4'],
        },
    ],
};

export const SEARCH_QUERY = { ru: 'Толстой', en: 'Austen' };

function wireBook(b, lang) {
    return {
        id: b.id,
        title: b.title,
        authors: [{ id: b.id * 10, full_name: b.author }],
        series: b.series ? [{ id: b.id * 7, ser: b.series, ser_no: 0 }] : [],
        genres: [{ id: 1, genre: b.genre }],
        annotation: b.annotation,
        filename: `${b.id}.fb2`,
        cover: true,
        registerdate: new Date(Date.now() - 30 * 24 * 3600 * 1000).toISOString(),
        docdate: new Date(Date.now() - 30 * 24 * 3600 * 1000).toISOString(),
        lang,
        fav: false,
        approved: true,
        path: 'video-fixtures.zip',
        format: 'fb2',
        favorite_count: 0,
        publisher: null,
        isbn: [],
    };
}

/** A typographic cover: a flat colour block, the title and the author. */
function coverSvg(b) {
    const [bg, fg] = b.palette;
    const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;');
    const words = b.title.split(' ');
    const lines = [];
    for (const w of words) {
        const last = lines[lines.length - 1];
        if (last && (last + ' ' + w).length <= 12) lines[lines.length - 1] = last + ' ' + w;
        else lines.push(w);
    }
    const title = lines
        .map((l, i) => `<tspan x="24" dy="${i === 0 ? 0 : 34}">${esc(l)}</tspan>`)
        .join('');
    return `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="360" viewBox="0 0 240 360">
<rect width="240" height="360" fill="${bg}"/>
<rect x="14" y="14" width="212" height="332" fill="none" stroke="${fg}" stroke-opacity=".35"/>
<text x="24" y="70" fill="${fg}" font-family="Georgia, serif" font-size="13" letter-spacing="2">${esc(b.author.toUpperCase())}</text>
<text x="24" y="150" fill="${fg}" font-family="Georgia, serif" font-size="28">${title}</text>
</svg>`;
}

export function makeUser(lang, { kindleEmail = '' } = {}) {
    return {
        username: 'reader',
        first_name: lang === 'ru' ? 'Аня' : 'Ann',
        last_name: '',
        is_superuser: false,
        books_lang: lang,
        interface_lang: lang,
        have_favs: false,
        has_bot_token: false,
        date_joined: new Date(Date.now() - 400 * 24 * 3600 * 1000).toISOString(),
        kindle_email: kindleEmail,
        kindle_sender: SENDER_ADDRESS,
        show_kindle_button: true,
        show_telegram_button: true,
    };
}

/**
 * installApiMocks answers every /api request of `page`. `state.kindleEmail`
 * is the user's Kindle address ('' until the profile saves one).
 */
export async function installApiMocks(page, lang, state) {
    const books = BOOKS[lang].map((b) => wireBook(b, lang));
    const json = (route, body, status = 200) =>
        route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

    await page.route('**/books-posters/**', (route) => {
        const id = Number(route.request().url().match(/\/(\d+)-fb2\.jpg/)?.[1]);
        const b = BOOKS[lang].find((x) => x.id === id) ?? BOOKS[lang][0];
        return route.fulfill({ status: 200, contentType: 'image/svg+xml', body: coverSvg(b) });
    });

    const isApi = (url) => url.pathname.startsWith('/api/') && url.pathname !== '/api/ws';
    await page.route(isApi, async (route) => {
        const req = route.request();
        const url = new URL(req.url());
        const path = url.pathname.replace(/^\/api/, '');
        const user = () => makeUser(lang, { kindleEmail: state.kindleEmail });

        switch (path) {
            case '/init':
                return json(route, { csrf_token: 'video-csrf', user: user() });
            case '/csrf-token':
                return json(route, { csrf_token: 'video-csrf' });
            case '/books/self-user':
                return json(route, user());
            case '/books/change-me':
                return json(route, user());
            case '/books/kindle': {
                if (req.method() === 'DELETE') {
                    state.kindleEmail = '';
                    return json(route, user());
                }
                const address = String(req.postDataJSON()?.kindle_email ?? '').trim().toLowerCase();
                if (!/@(free\.)?kindle\.com$/.test(address)) {
                    return json(route, { error: 'invalid_kindle_email' }, 400);
                }
                state.kindleEmail = address;
                return json(route, user());
            }
            case '/books/langs':
                return json(route, {
                    langs: [
                        { lang: 'ru', language_count: 412000 },
                        { lang: 'en', language_count: 61000 },
                    ],
                });
            case '/books/theme':
                return req.method() === 'GET' ? json(route, { theme: 'light' }) : json(route, {});
            case '/books/list':
                return json(route, { books, length: books.length });
            case '/status':
                return json(route, { result: 'ok' });
            case '/donate':
                return json(route, []);
            case '/telegram/bot/status':
                return json(route, { has_bot_token: false });
            case '/books/autocomplete':
                return json(route, []);
            default:
                if (path.startsWith('/books/collections') || path.startsWith('/collections')) {
                    return json(route, []);
                }
                state.unmocked?.add(`${req.method()} ${path}`);
                return json(route, {});
        }
    });

    // The websocket: answer a Kindle send with "sent" after a short beat, as
    // the relay would; everything else (subscriptions, conversions) is dropped.
    await page.routeWebSocket(/\/api\/ws$/, (ws) => {
        ws.onMessage((raw) => {
            let frame;
            try {
                frame = JSON.parse(String(raw));
            } catch {
                return;
            }
            if (frame.type === 'kindle') {
                setTimeout(
                    () => ws.send(JSON.stringify({ type: 'kindle', bookID: frame.bookID, status: 'sent' })),
                    1200,
                );
            }
        });
    });
}
