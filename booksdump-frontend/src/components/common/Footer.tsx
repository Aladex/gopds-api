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
            <div className="left">
                <a href="https://aladex.ru/donat-za-bibliotieku/" target="_blank" rel="noopener noreferrer">Donate</a>
            </div>
            <div className="right">
                <p>App Version: {appVersion}</p>
            </div>
        </footer>
    );
};

export default Footer;