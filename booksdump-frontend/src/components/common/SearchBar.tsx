import React from 'react';
import {
    Box,
    Stack,
    Card,
    CardContent,
    Button,
    Select,
    MenuItem,
    InputLabel
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { Favorite, FavoriteBorder } from "@mui/icons-material";
import { useNavigate } from 'react-router-dom';
import { useFav } from "../../context/FavContext";
import { useAuthor } from "../../context/AuthorContext";
import { useSearchBar } from "../../context/SearchBarContext";
import { StyledFormControl} from "../StyledDataItems";
import useSearchOptions from "../hooks/useSearchOptions";
import AutocompleteSearch from "./AutocompleteSearch";

interface Record {
    option: string;
    path: string;
}

const SearchBar: React.FC = () => {
    const { t } = useTranslation();
    const { searchItem, setSearchItem, selectedSearch, setSelectedSearch } = useSearchBar();
    const navigate = useNavigate();
    const { fav, favEnabled, setFav } = useFav();
    const searchOptions = useSearchOptions(setSelectedSearch);

    const records: Record[] = [
        { option: 'authorsBookSearch', path: `/books/find/author/` },
        { option: 'title', path: `/books/find/title/${encodeURIComponent(searchItem)}/1` },
        { option: 'author', path: `/authors/${encodeURIComponent(searchItem)}/1` },
    ];
    const {authorId, setAuthorBook, clearAuthorId, clearAuthorBook } = useAuthor();

    const handleSetAuthorBook = () => {
        setAuthorBook(searchItem);
    };

    const setFavContext = (newFav: boolean) => {
        if (favEnabled) {
            setFav(newFav);
        }
    }

    const navigateToSearchResults = () => {
        // Check that the search field is not empty and contains at least one character
        if (!searchItem || searchItem.trim().length === 0) {
            return;
        }

        const record = records.find(record => record.option === selectedSearch);
        handleSetAuthorBook();
        if (record) {
            // If option is 'authorsBookSearch', set authorId in AuthorContext, else clear it
            if (record.option !== 'authorsBookSearch') {
                clearAuthorId();
                clearAuthorBook();
                navigate(record.path);
            } else {
                navigate(record.path + authorId + '/1');
            }
        }
    };

    return (
        <Box maxWidth={1200} mx="auto">
            <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                <CardContent>
                    <Stack
                        direction={{ xs: 'column', lg: 'row' }}
                        spacing={2}
                        alignItems={{ xs: 'stretch', lg: 'flex-start' }}
                    >
                        <Box sx={{ flex: { xs: 1, lg: 3 } }}>
                            <Stack direction={{ xs: 'column', lg: 'row' }} spacing={2}>
                                <Box sx={{ flex: { xs: 1, lg: 1 } }}>
                                            <StyledFormControl
                                                fullWidth
                                                sx={{
                                                    '& .MuiOutlinedInput-root': {
                                                        '& fieldset': {
                                                            borderColor: 'rgba(0, 0, 0, 0.23)',
                                                        },
                                                        '&:hover fieldset': {
                                                            borderColor: 'black',
                                                        },
                                                        '&.Mui-focused fieldset': {
                                                            borderColor: 'black',
                                                        },
                                                    },
                                                    '& .MuiInputLabel-root': {
                                                        color: '#2f2f2f',
                                                    },
                                                    '& .MuiInputLabel-root.Mui-focused': {
                                                        color: '#2f2f2f',
                                                    },
                                                }}
                                            >
                                                <InputLabel id="category-search-label">{t('categorySearch')}</InputLabel>
                                                <Select
                                                    labelId="category-search-label"
                                                    value={selectedSearch}
                                                    onChange={(e) => setSelectedSearch(e.target.value as string)}
                                                    disabled={fav}
                                                    label={t('categorySearch')}
                                                >
                                                    {searchOptions.map((option) => (
                                                        <MenuItem key={option.value} value={option.value}>
                                                            {option.label}
                                                        </MenuItem>
                                                    ))}
                                                </Select>
                                            </StyledFormControl>
                                </Box>
                                <Box sx={{ flex: { xs: 1, lg: 2 } }}>
                                    <AutocompleteSearch
                                        value={searchItem}
                                        onChange={setSearchItem}
                                        searchType={selectedSearch}
                                        disabled={fav}
                                        onEnterPressed={navigateToSearchResults}
                                        placeholder={t('searchItem')}
                                    />
                                </Box>
                            </Stack>
                        </Box>

                        <Box sx={{ flex: { xs: 1, lg: 1 } }}>
                            <Stack direction="row" spacing={1} alignItems="center">
                                <Box sx={{ flex: { xs: 2, lg: 1 } }}>
                                    <Button
                                        sx={{
                                            height: '56px',
                                            minHeight: '56px'
                                        }}
                                        variant="contained"
                                        onClick={navigateToSearchResults}
                                        disabled={fav}
                                        fullWidth
                                    >
                                        {t('search')}
                                    </Button>
                                </Box>
                                <Box sx={{
                                    flex: { xs: 1, lg: 1 },
                                    display: 'flex',
                                    justifyContent: 'center',
                                    alignItems: 'center'
                                }}>
                                    <Box
                                        onClick={() => favEnabled && setFavContext(!fav)}
                                        sx={{
                                            display: 'flex',
                                            alignItems: 'center',
                                            justifyContent: 'center',
                                            height: '56px',
                                            cursor: favEnabled ? 'pointer' : 'default',
                                            opacity: favEnabled ? 1 : 0.38,
                                        }}
                                    >
                                        {fav ? (
                                            <Favorite
                                                sx={{
                                                    fontSize: '48px',
                                                    color: '#2f2f2f',
                                                    filter: 'drop-shadow(0px 2px 1px rgba(0,0,0,0.2)) drop-shadow(0px 1px 1px rgba(0,0,0,0.14)) drop-shadow(0px 1px 3px rgba(0,0,0,0.12))',
                                                    '&:hover': {
                                                        filter: 'drop-shadow(0px 3px 5px rgba(0,0,0,0.2)) drop-shadow(0px 6px 10px rgba(0,0,0,0.14)) drop-shadow(0px 1px 18px rgba(0,0,0,0.12))',
                                                    }
                                                }}
                                            />
                                        ) : (
                                            <FavoriteBorder
                                                sx={{
                                                    fontSize: '48px',
                                                    color: '#2f2f2f',
                                                    filter: 'drop-shadow(0px 2px 1px rgba(0,0,0,0.2)) drop-shadow(0px 1px 1px rgba(0,0,0,0.14)) drop-shadow(0px 1px 3px rgba(0,0,0,0.12))',
                                                    '&:hover': {
                                                        filter: 'drop-shadow(0px 3px 5px rgba(0,0,0,0.2)) drop-shadow(0px 6px 10px rgba(0,0,0,0.14)) drop-shadow(0px 1px 18px rgba(0,0,0,0.12))',
                                                        color: '#2f2f2f',
                                                    }
                                                }}
                                            />
                                        )}
                                    </Box>
                                </Box>
                            </Stack>
                        </Box>
                    </Stack>
                </CardContent>
            </Card>
        </Box>
    );
};

export default SearchBar;
