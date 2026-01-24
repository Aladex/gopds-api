export async function downloadViaIframe(
    url: string,
    onError?: (status: number) => void
): Promise<void> {
    try {
        const response = await fetch(url, {
            method: 'GET',
            credentials: 'include',
            headers: {
                Range: 'bytes=0-0',
            },
        });
        if (!response.ok && response.status !== 206) {
            if (onError) {
                onError(response.status);
            }
            return;
        }
    } catch (_) {
        if (onError) {
            onError(0);
        }
        return;
    }

    const iframe = document.createElement('iframe');
    iframe.style.display = 'none';
    iframe.src = url;
    document.body.appendChild(iframe);

    iframe.onload = () => {
        setTimeout(() => document.body.removeChild(iframe), 1000);
    };
}
