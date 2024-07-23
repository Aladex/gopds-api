import React from 'react';
import {Typography} from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSearchBar } from '../../context/SearchBarContext';
import { useAuthor} from "../../context/AuthorContext";
import { useCommonStyles } from '../themeStyles';

interface Author {
    id: number;
    full_name: string;
}

interface AuthorsListProps {
    authors: Author[];
}

const AuthorsList: React.FC<AuthorsListProps> = ({authors}) => {
    const classes = useCommonStyles();
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { t } = useTranslation();
    const { clearAuthorBook } = useAuthor();

    const navigateToAuthor = (authorId: number) => {
        setSearchItem('');
        clearAuthorBook();
        navigate(`/books/find/author/${authorId}/1`);
    }

    return (
        <>
            <Typography variant="subtitle1">Authors:</Typography>
            <Typography variant="body2">
                {authors && authors.length > 0 ? (
                    authors.map((author: Author) => (
                        <React.Fragment key={author.id}>
                            <span> &#8226; </span>
                            <button onClick={() => navigateToAuthor(author.id)} className={classes.buttonLink}>
                                {author.full_name}
                            </button>
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