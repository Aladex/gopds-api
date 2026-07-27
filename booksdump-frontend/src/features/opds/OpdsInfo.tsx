import React from 'react';
import { useTranslation } from 'react-i18next';

/**
 * OpdsInfo explains how to point a reading application at this catalogue.
 *
 * It is a page of prose, so the paragraphs are styled together rather than one
 * by one.
 */
const OpdsInfo: React.FC = () => {
    const { t } = useTranslation();
    const domain = window.location.hostname;

    return (
        <div className="mx-auto w-full max-w-[1200px] py-1.5">
            <div className="rounded border border-border bg-card p-6">
                <h2 className="mb-3 text-lg font-medium">{t('opdsInfo.title')}</h2>
                <div className="flex flex-col gap-2 text-sm text-muted-foreground [&_b]:text-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-foreground">
                    <p>{t('opdsInfo.addCatalog', { domain })}</p>
                    <p>{t('opdsInfo.testMode')}</p>
                    <p>{t('opdsInfo.authorization')}</p>
                    <p>{t('opdsInfo.alReaderAuth')}</p>
                    <p>{t('opdsInfo.tlsSupport')}</p>
                    <p>{t('opdsInfo.pocketBook')}</p>
                    <p>{t('opdsInfo.onyxBoox')}</p>
                </div>
            </div>
        </div>
    );
};

export default OpdsInfo;
