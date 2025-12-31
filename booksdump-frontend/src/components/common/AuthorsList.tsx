import React from 'react';
import {Typography, Box} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSearchBar } from '../../context/SearchBarContext';
import { useAuthor} from "../../context/AuthorContext";
import { buttonLinkSx } from '../commonStyles';
import {useFav} from "../../context/FavContext";

interface Author {
    id: number;
    full_name: string;
}

interface AuthorsListProps {
    authors: Author[];
}

const AuthorsList: React.FC<AuthorsListProps> = ({authors}) => {
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { t } = useTranslation();
    const { clearAuthorBook } = useAuthor();
    const { setFav } = useFav();

    const navigateToAuthor = (authorId: number) => {
        setSearchItem('');
        setFav(false);
        clearAuthorBook();
        navigate(`/books/find/author/${authorId}/1`);
    }

    return (
        <>
            <Typography variant="subtitle1">{t('authors')}:</Typography>
            <Typography variant="body2" sx={{ color: 'text.primary' }}>
                {authors && authors.length > 0 ? (
                    authors.map((author: Author) => (
                        <React.Fragment key={author.id}>
                            <span> &#8226; </span>
                            <Box
                                component="button"
                                onClick={() => navigateToAuthor(author.id)}
                                sx={(theme) => ({
                                    ...buttonLinkSx(theme),
                                    color: theme.palette.text.primary,
                                })}
                            >
                                {author.full_name}
                            </Box>
                        </React.Fragment>
                    ))
                ) : (
                    <span>{t('noAuthor')}</span>
                )}
            </Typography>
        </>
    );
};

export default AuthorsList;
