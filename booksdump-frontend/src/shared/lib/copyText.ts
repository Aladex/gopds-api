/**
 * copyText puts a string on the clipboard, by whichever means the page has.
 *
 * navigator.clipboard exists only in a secure context, and this application is
 * self-hosted: a library served over plain http on somebody's network is an
 * ordinary way to run it, and there the API is not merely refused — it is
 * undefined, so reaching for it throws. The old selection dance still works
 * there, and is the whole reason this is not a one-liner.
 */
export async function copyText(value: string): Promise<boolean> {
    if (navigator.clipboard?.writeText) {
        try {
            await navigator.clipboard.writeText(value);
            return true;
        } catch {
            // Refused, or no permission. Fall through and try the other way.
        }
    }

    try {
        const active = document.activeElement as HTMLElement | null;

        /*
          Inside the dialog, not on the body. A dialog keeps focus within
          itself, so a textarea appended outside one never takes the selection
          — and execCommand then reports success having copied nothing, which
          is worse than failing: the tick appears and the clipboard is
          unchanged. Measured with the dialog open: a textarea on the body
          selects 0 characters, one inside it selects all of them.
        */
        const host = active?.closest?.('[role="dialog"]') ?? document.body;

        const area = document.createElement('textarea');
        area.value = value;
        area.setAttribute('readonly', '');
        // Off-screen but focusable: a hidden element cannot be selected.
        area.style.cssText = 'position:fixed;top:0;left:0;opacity:0';
        host.appendChild(area);
        area.select();
        const copied = document.execCommand('copy');
        area.remove();

        // Selecting the textarea took the focus off whatever was clicked.
        active?.focus?.();

        return copied;
    } catch {
        return false;
    }
}
