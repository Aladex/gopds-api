import React from 'react';
import { Typography, Box } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSearchBar } from '../../context/SearchBarContext';
import { buttonLinkSx } from '../commonStyles';
import {useFav} from "../../context/FavContext";

interface Category {
    id: number;
    ser: string;
    ser_no: number;
}

interface CategoriesListProps {
    categories: Category[];
}

const CategoriesList: React.FC<CategoriesListProps> = ({ categories }) => {
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { t } = useTranslation();
    const { setFav } = useFav();

    const navigateToCategory = (categoryId: number) => {
        setSearchItem('');
        setFav(false);
        navigate(`/books/find/category/${categoryId}/1`);
    };

    if (!categories || categories.length === 0) {
        return null;
    }

    return (
        <div>
            <Typography variant="subtitle1">{t('categories')}:</Typography>
            <Typography variant="body2">
                {categories.map((category: Category) => (
                    <React.Fragment key={category.id}>
                        <span> &#8226; </span>
                        <Box component="button" onClick={() => navigateToCategory(category.id)} sx={buttonLinkSx}>
                            {category.ser}
                        </Box>
                    </React.Fragment>
                ))}
            </Typography>
        </div>
    );
};

export default CategoriesList;