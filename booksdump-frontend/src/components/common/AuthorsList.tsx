import React from 'react';
import {Typography} from '@mui/material';
import {makeStyles} from '@mui/styles';
import {Theme} from '@mui/material/styles';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSearchBar } from '../../context/SearchBarContext';
import { useAuthor} from "../../context/AuthorContext";

interface Author {
    id: number;
    full_name: string;
}

interface AuthorsListProps {
    authors: Author[];
}

const useStyles = makeStyles((theme: Theme) => ({
    link: {
        color: theme.palette.secondary.main,
        textDecoration: 'none',
        '&:hover': {
            textDecoration: 'underline',
        },
    },
    buttonLink: { // Adding the buttonLink class
        background: 'none',
        border: 'none',
        padding: 0,
        color: theme.palette.secondary.main,
        textDecoration: 'none',
        cursor: 'pointer',
        '&:hover': {
            textDecoration: 'underline',
        },
    },
}));

const AuthorsList: React.FC<AuthorsListProps> = ({authors}) => {
    const classes = useStyles();
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