import React, { useEffect, useState } from 'react';

import * as systemApi from '@/api/system';

/** Footer carries the build the reader is looking at, and nothing else. */
const Footer: React.FC = () => {
    const [appVersion, setAppVersion] = useState('');

    useEffect(() => {
        systemApi
            .getStatus()
            .then(({ result }) => setAppVersion(result))
            .catch((error) => console.error('Error fetching app version:', error));
    }, []);

    return (
        <footer className="mt-auto flex w-full items-center justify-center bg-neutral-800 px-2.5 py-0.5 text-[10px] text-white">
            App Version: {appVersion}
        </footer>
    );
};

export default Footer;
