import React from 'react';
import {Typography} from '@mui/material';
import {makeStyles} from '@mui/styles';
import {Theme} from '@mui/material/styles';
import { useNavigate } from 'react-router-dom';

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
}));

const AuthorsList: React.FC<AuthorsListProps> = ({authors}) => {
    const classes = useStyles();
    const navigate = useNavigate();

    return (
        <>
            <Typography variant="subtitle1">Authors:</Typography>
            <Typography variant="body2">
                {authors.map((author: Author) => (
                    <React.Fragment key={author.id}>
                        <span> &#8226; </span>
                        <a href="#" onClick={() => navigate(`/books/find/author/${author.id}/1`)} className={classes.link}>
                            {author.full_name}
                        </a>
                    </React.Fragment>
                ))}
            </Typography>
        </>
    );
};

export default AuthorsList;