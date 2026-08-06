import React from 'react';
import { useTranslation } from 'react-i18next';

/**
 * OpdsConnection says how to point a reading application at this catalogue.
 *
 * Two sentences: the address, and that signing in is required. It used to carry
 * five more — that OPDS was in test mode, that FBReader might misbehave, that
 * PocketBook and Onyx Boox readers too old for TLS 1.2 would fail — none of
 * which has been true for years. A page of stale caveats reads as a warning
 * about the service rather than as instructions for using it.
 *
 * The address is built from the host being served, so an instance running
 * anywhere names itself correctly.
 */
export const OpdsConnection: React.FC = () => {
    const { t } = useTranslation();
    const url = `${window.location.origin}/opds/`;

    return (
        <div className="flex flex-col gap-2 text-sm text-muted-foreground">
            <p>
                <WithAddress text={t('opdsInfo.addCatalog', { url })} address={url} />
            </p>
            <p>{t('opdsInfo.authorization')}</p>
        </div>
    );
};

/**
 * WithAddress sets the one part of a sentence that is an address rather than
 * prose.
 *
 * The sentence arrives from the translation as a single string and the address
 * is found within it, rather than the sentence being assembled here out of
 * fragments: a translator moving the address to the other end of the sentence
 * must not have to know anything about how it is styled.
 */
const WithAddress: React.FC<{ text: string; address: string }> = ({ text, address }) => {
    const at = text.indexOf(address);
    if (at < 0) {
        return <>{text}</>;
    }
    return (
        <>
            {text.slice(0, at)}
            <code className="rounded bg-muted px-1 py-0.5 text-foreground">{address}</code>
            {text.slice(at + address.length)}
        </>
    );
};

/**
 * OpdsInfo is the same thing as a page of its own.
 *
 * Where it belongs now is the profile, beside the Telegram bot: both answer the
 * one question "how do I get the books into something of mine". This route is
 * kept because it was in the navigation for years and will be bookmarked.
 */
const OpdsInfo: React.FC = () => {
    const { t } = useTranslation();

    return (
        <div className="mx-auto w-full max-w-[1200px] py-1.5">
            <div className="rounded border border-border bg-card p-6">
                <h2 className="mb-3 text-lg font-medium">{t('opdsInfo.title')}</h2>
                <OpdsConnection />
            </div>
        </div>
    );
};

export default OpdsInfo;
