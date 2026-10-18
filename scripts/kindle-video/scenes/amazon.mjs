// Scenes 2 and 3: the stylised "Manage Your Content and Devices" mock in
// amazon/mycd.html. Scene 3 continues on scene 2's page.
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { KINDLE_ADDRESS, SENDER_ADDRESS } from '../lib/mocks.mjs';

const MOCK = path.join(import.meta.dirname, '..', 'amazon', 'mycd.html');

const PEOPLE = {
    ru: { name: 'Anya', device: 'Anya’s Kindle', approved: 'anya.reader@example.com' },
    en: { name: 'Ann', device: 'Ann’s Kindle', approved: 'ann.reader@example.com' },
};

export const amazonPreferences = {
    id: 'amazon-preferences',
    step: 2,
    async open(env) {
        const who = PEOPLE[env.lang];
        const url = pathToFileURL(MOCK);
        url.search = new URLSearchParams({
            name: who.name,
            device: who.device,
            kindle: KINDLE_ADDRESS,
            approved: who.approved,
        }).toString();
        const page = await env.context.newPage();
        await page.goto(url.href);
        await page.waitForTimeout(400);
        return page;
    },
    async play(shot, env) {
        const { page } = env;
        await shot.hold(400);
        await shot.caption('openAmazon');
        await shot.move(page.locator('h1'), { dx: -200 });
        await shot.hold(1200);

        await shot.caption('preferences');
        await shot.click(page.getByRole('tab', { name: 'Preferences' }));
        await shot.hold(400);
        await shot.click(page.locator('#pds-head'));
        await shot.hold(500);
        await shot.scrollTo(page.locator('#pds-head'), { block: 0.08 });

        await shot.caption('kindleAddress');
        const address = page.locator('#kindle-address');
        await shot.move(address);
        await shot.zoom(page.locator('#devices'), 1.4);
        await shot.hold(1300);
        await shot.end(0);
    },
};

export const amazonApprove = {
    id: 'amazon-approve',
    step: 3,
    continues: true,
    async play(shot, env) {
        const { page } = env;
        await shot.caption('addNew');
        await shot.unzoom({ duration: 0.6 });
        await shot.scrollTo(page.getByText('Approved Personal Document E-mail List'), { block: 0.25 });
        await shot.zoom(page.locator('#approved'), 1.4);
        await shot.click(page.locator('#add-link'));
        await shot.unzoom({ duration: 0.6 });

        await shot.caption('pasteSender');
        const input = page.locator('#new-address');
        await shot.click(input);
        await shot.keys('Ctrl', 'V');
        await page.keyboard.press('ControlOrMeta+V');
        const pasted = await input.inputValue();
        if (pasted !== SENDER_ADDRESS) throw new Error(`pasted "${pasted}" into Amazon`);
        await shot.zoom(page.locator('.modal'), 1.6);
        await shot.hold(700);
        await shot.click(page.locator('#add-address'));
        await shot.unzoom({ duration: 0.7 });

        await shot.caption('approved');
        const fresh = page.locator('#approved tr.fresh');
        await shot.move(fresh, { dx: -120 });
        await shot.zoom(page.locator('#approved'), 1.4);
        await shot.hold(900);
        await shot.unzoom({ duration: 0.6 });

        await shot.caption('copyKindle');
        const address = page.locator('#kindle-address');
        await shot.scrollTo(page.locator('#devices'), { block: 0.3 });
        await shot.zoom(page.locator('#devices'), 1.4);
        // A triple click selects the whole cell — what a reader would do.
        const p = await shot.move(address);
        await page.mouse.click(p.x, p.y, { clickCount: 3 });
        await shot.hold(400);
        await shot.keys('Ctrl', 'C');
        await page.keyboard.press('ControlOrMeta+C');
        await shot.hold(1100);
        await shot.end(0);
    },
};
