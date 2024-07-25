// src/components/common/Footer.tsx
import React, { useEffect, useState } from 'react';
import '../styles/Footer.css';
import { fetchWithAuth } from '../../api/config';

const Footer: React.FC = () => {
    const [appVersion, setAppVersion] = useState<string>('');

    useEffect(() => {
        const fetchAppVersion = async () => {
            try {
                const response = await fetchWithAuth.get('/status');
                setAppVersion(response.data.result);
            } catch (error) {
                console.error('Error fetching app version:', error);
            }
        };

        fetchAppVersion();
    }, []);

    return (
        <footer className="footer">
            <p>App Version: {appVersion}</p>
        </footer>
    );
};

export default Footer;