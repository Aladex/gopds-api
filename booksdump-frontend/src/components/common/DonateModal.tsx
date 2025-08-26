import React, { useState } from 'react';
import '../styles/DonateModal.css';

interface DonateModalProps {
    open: boolean;
    onClose: () => void;
}

const DonateModal: React.FC<DonateModalProps> = ({ open, onClose }) => {
    const [activeTab, setActiveTab] = useState<'tinkoff' | 'crypto' | 'paypal' | 'buymeacoffee'>('tinkoff');

    if (!open) return null;

    const copyToClipboard = (text: string) => {
        navigator.clipboard.writeText(text);
        // Можно добавить уведомление о копировании
    };

    return (
        <div className="donate-modal-overlay" onClick={onClose}>
            <div className="donate-modal" onClick={e => e.stopPropagation()}>
                <div className="donate-modal-header">
                    <h2>Поддержать проект</h2>
                    <button className="close-button" onClick={onClose}>×</button>
                </div>
                
                <div className="donate-tabs">
                    <button 
                        className={`tab ${activeTab === 'tinkoff' ? 'active' : ''}`}
                        onClick={() => setActiveTab('tinkoff')}
                    >
                        Tinkoff
                    </button>
                    <button 
                        className={`tab ${activeTab === 'crypto' ? 'active' : ''}`}
                        onClick={() => setActiveTab('crypto')}
                    >
                        Криптовалюта
                    </button>
                    <button 
                        className={`tab ${activeTab === 'paypal' ? 'active' : ''}`}
                        onClick={() => setActiveTab('paypal')}
                    >
                        PayPal
                    </button>
                    <button 
                        className={`tab ${activeTab === 'buymeacoffee' ? 'active' : ''}`}
                        onClick={() => setActiveTab('buymeacoffee')}
                    >
                        BuyMeACoffee
                    </button>
                </div>

                <div className="donate-content">
                    {activeTab === 'tinkoff' && (
                        <div className="donate-option">
                            <h3>Отправить деньги на Tinkoff</h3>
                            <div className="card-info">
                                <p>Номер карты:</p>
                                <div className="copy-field">
                                    <span className="card-number">5536 9139 9418 6852</span>
                                    <button 
                                        className="copy-button"
                                        onClick={() => copyToClipboard('5536913994186852')}
                                    >
                                        Копировать
                                    </button>
                                </div>
                            </div>
                            <p className="donate-note">Или по ссылке:</p>
                            <a
                                href="https://tbank.ru/cf/634wAzuZc0Z"
                                target="_blank"
                                rel="noopener noreferrer"
                                className="donate-link"
                            >
                                Т-БАНК
                            </a>
                        </div>
                    )}

                    {activeTab === 'crypto' && (
                        <div className="donate-option">
                            <h3>Отправить донат криптовалютой</h3>
                            <div className="crypto-addresses">
                                <div className="crypto-item">
                                    <p><strong>Bitcoin:</strong></p>
                                    <div className="copy-field">
                                        <span className="crypto-address">bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f</span>
                                        <button 
                                            className="copy-button"
                                            onClick={() => copyToClipboard('bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f')}
                                        >
                                            Копировать
                                        </button>
                                    </div>
                                </div>
                                <div className="crypto-item">
                                    <p><strong>Ethereum:</strong></p>
                                    <div className="copy-field">
                                        <span className="crypto-address">0xD053A0fE7C450b57da9FF169620EB178644b54C9</span>
                                        <button 
                                            className="copy-button"
                                            onClick={() => copyToClipboard('0xD053A0fE7C450b57da9FF169620EB178644b54C9')}
                                        >
                                            Копировать
                                        </button>
                                    </div>
                                </div>
                                <div className="crypto-item">
                                    <p><strong>USDT (TRON):</strong></p>
                                    <div className="copy-field">
                                        <span className="crypto-address">TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec</span>
                                        <button 
                                            className="copy-button"
                                            onClick={() => copyToClipboard('TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec')}
                                        >
                                            Копировать
                                        </button>
                                    </div>
                                </div>
                            </div>
                        </div>
                    )}

                    {activeTab === 'paypal' && (
                        <div className="donate-option">
                            <h3>Отправить донат через PayPal</h3>
                            <a 
                                href="https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62" 
                                target="_blank" 
                                rel="noopener noreferrer"
                                className="donate-link"
                            >
                                Открыть PayPal
                            </a>
                        </div>
                    )}

                    {activeTab === 'buymeacoffee' && (
                        <div className="donate-option">
                            <h3>Отправить донат через BuyMeACoffee</h3>
                            <a 
                                href="https://www.buymeacoffee.com/aladex" 
                                target="_blank" 
                                rel="noopener noreferrer"
                                className="donate-link"
                            >
                                Открыть BuyMeACoffee
                            </a>
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
};

export default DonateModal;
