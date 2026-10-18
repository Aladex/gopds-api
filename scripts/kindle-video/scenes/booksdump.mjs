// Scenes on booksdump: the real UI from `vite` dev, every API call and the
// websocket answered by lib/mocks.mjs. Copy and paste go through the browser's
// real clipboard, so what is pasted is what the page copied.
import { installApiMocks, KINDLE_ADDRESS, SEARCH_QUERY, SENDER_ADDRESS } from '../lib/mocks.mjs';

export const ORIGIN = 'https://booksdump.com';

// Accessible names of the controls the scenes drive, per interface language
// (the same strings as the frontend's locales).
const L = {
    ru: {
        connections: 'Подключения',
        openSettings: 'Открыть настройки Kindle',
        copySender: 'Скопировать адрес отправителя',
        save: 'Сохранить',
    },
    en: {
        connections: 'Connections',
        openSettings: 'Open Kindle settings',
        copySender: 'Copy the sender address',
        save: 'Save',
    },
};

/**
 * Opens a booksdump page. The page lives at https://booksdump.com so every
 * address the UI derives from its own location reads as the real site; the
 * requests are served by the local vite dev server and the API mocks.
 */
async function openSite(env, path) {
    const page = await env.context.newPage();
    // Registered first, so it runs last: whatever the mocks do not answer.
    await page.route(`${ORIGIN}/**`, async (route) => {
        const u = new URL(route.request().url());
        const response = await route.fetch({ url: env.viteUrl + u.pathname + u.search });
        await route.fulfill({ response });
    });
    await installApiMocks(page, env.lang, env.state);
    await page.goto(ORIGIN + path);
    await page.locator('h2, h3').first().waitFor();
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(600);
    return page;
}

const searchPath = (lang) => `/books/find/title/${encodeURIComponent(SEARCH_QUERY[lang])}/1`;

async function expectValue(locator, expected) {
    const value = (await locator.inputValue()).trim();
    if (value !== expected) throw new Error(`pasted "${value}", expected "${expected}"`);
}

export const setupDialog = {
    id: 'setup-dialog',
    step: null,
    async open(env) {
        env.state.kindleEmail = '';
        return openSite(env, searchPath(env.lang));
    },
    async play(shot, env) {
        const { page, lang } = env;
        await shot.hold(400);
        await shot.caption('kindleMuted');
        const kindle = page.locator('button[data-configured="false"]').first();
        await shot.move(kindle);
        await shot.zoom(kindle, 1.8);
        await shot.hold(500);
        await shot.click(kindle);
        await shot.unzoom({ duration: 0.7 });

        await shot.caption('openSettings');
        const open = page.getByRole('button', { name: L[lang].openSettings });
        await shot.move(open);
        await shot.hold(2600);
        await shot.click(open);
        // The profile opens on Connections, scrolled to the Kindle group.
        await page.locator('#profile-kindle').waitFor();
        await shot.end(0);
    },
};

export const profileCopy = {
    id: 'profile-copy',
    step: 1,
    continues: true,
    async play(shot, env) {
        const { page, lang } = env;
        await shot.caption('copySender');
        const sender = page.getByTestId('kindle-sender');
        await shot.move(sender);
        await shot.zoom(sender.locator('..'), 1.6);
        shot.mark('poster');
        await shot.hold(600);
        await shot.click(page.getByRole('button', { name: L[lang].copySender }));
        const copied = await page.evaluate(() => navigator.clipboard.readText());
        if (copied !== SENDER_ADDRESS) throw new Error(`clipboard holds "${copied}"`);
        await shot.hold(1000);
        await shot.end(0);
    },
};

export const profilePaste = {
    id: 'profile-paste',
    step: 4,
    open: (env) => openSite(env, searchPath(env.lang)),
    async play(shot, env) {
        const { page, lang } = env;
        await shot.hold(400);
        await shot.caption('backToProfile');
        await shot.click(page.getByRole('button', { name: 'reader' }));
        const tab = page.getByRole('tab', { name: L[lang].connections });
        await tab.waitFor();
        await shot.hold(300);
        await shot.click(tab);
        await shot.scrollTo(page.locator('#profile-kindle'), { block: 0.02 });

        await shot.caption('pasteKindle');
        const group = page.locator('#profile-kindle');
        const input = page.locator('#profile-kindle-email');
        await shot.zoom(input, 1.6);
        await shot.click(input);
        await shot.keys('Ctrl', 'V');
        await page.keyboard.press('ControlOrMeta+V');
        await expectValue(input, KINDLE_ADDRESS);
        await shot.hold(700);
        await shot.click(group.getByRole('button', { name: L[lang].save, exact: true }));
        await shot.hold(400);

        await shot.caption('connected');
        await shot.zoom(group.locator('h3'), 1.7);
        await shot.hold(1200);
        await shot.end(0);
    },
};

export const sendBook = {
    id: 'send-book',
    step: 5,
    async open(env) {
        env.state.kindleEmail = KINDLE_ADDRESS;
        return openSite(env, searchPath(env.lang));
    },
    async play(shot, env) {
        const { page } = env;
        await shot.hold(400);

        // Captions go on top here: the toast takes the bottom-right corner.
        await shot.caption('kindleButton', { position: 'top' });
        const kindle = page.locator('button[data-configured="true"]').first();
        await shot.move(kindle);
        await shot.zoom(kindle, 1.8);
        await shot.hold(300);

        await shot.caption('sendBook', { position: 'top' });
        await shot.click(kindle);
        // The card sends {type:"kindle"} over the socket; the mock answers "sent".
        const toast = page.locator('[data-sonner-toast]').first();
        await toast.waitFor();
        await shot.hold(500);
        await shot.zoom(toast, 1.6);

        await shot.caption('arrives', { position: 'top' });
        await shot.hold(1500);
        await shot.end(300);
    },
};
