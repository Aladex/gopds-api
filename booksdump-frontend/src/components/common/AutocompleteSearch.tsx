import React, { useState, useEffect, useMemo, useRef } from 'react';
import {
  Autocomplete,
  TextField,
  Box,
  Typography,
  Chip,
  CircularProgress
} from '@mui/material';
import { Book, Person } from '@mui/icons-material';
import { autocompleteService, AutocompleteSuggestion } from '../../api/autocomplete';
import { useTranslation } from 'react-i18next';
import { useAuthor } from '../../context/AuthorContext';
import { useSearchBar } from '../../context/SearchBarContext';
import { useNavigate } from 'react-router-dom';

interface AutocompleteSearchProps {
  value: string;
  onChange: (value: string) => void;
  searchType: string;
  disabled?: boolean;
  onEnterPressed?: () => void;
  placeholder?: string;
}

const AutocompleteSearch: React.FC<AutocompleteSearchProps> = ({
  value,
  onChange,
  searchType,
  disabled = false,
  onEnterPressed,
  placeholder
}) => {
  const { t } = useTranslation();
  const { authorId } = useAuthor();
  const { selectedLanguage } = useSearchBar();
  const [suggestions, setSuggestions] = useState<AutocompleteSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [inputValue, setInputValue] = useState(value);
  const navigate = useNavigate();
  const abortControllerRef = useRef<AbortController | null>(null);

  // Debounce function for request optimization
  const debounce = (func: Function, delay: number) => {
    let timeoutId: NodeJS.Timeout;
    return (...args: any[]) => {
      clearTimeout(timeoutId);
      timeoutId = setTimeout(() => func.apply(null, args), delay);
    };
  };

  // Memoized function for fetching suggestions
  const fetchSuggestions = useMemo(
    () => debounce(async (query: string, type: string, currentAuthorId: string, currentLanguage: string) => {
      // Cancel previous request if exists
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
      }

      if (!query || query.trim().length < 4) {
        setSuggestions([]);
        setLoading(false);
        return;
      }

      const trimmedQuery = query.trim();

      // Additional check to ensure query still matches current inputValue
      if (query !== inputValue) {
        setSuggestions([]);
        setLoading(false);
        return;
      }

      // Create new AbortController for this request
      abortControllerRef.current = new AbortController();

      setLoading(true);
      try {
        // Another check before sending request
        if (query !== inputValue || trimmedQuery.length < 4) {
          setSuggestions([]);
          setLoading(false);
          return;
        }

        // Map search types to API parameters
        let apiType: 'title' | 'author' | 'all';

        switch (type) {
          case 'title':
            apiType = 'title';
            break;
          case 'author':
            apiType = 'author';
            break;
          case 'authorsBookSearch':
            apiType = 'title';
            break;
          default:
            apiType = 'all';
        }

        // Pass authorId only for author's book search
        const authorIdParam = type === 'authorsBookSearch' && currentAuthorId ? currentAuthorId : undefined;

        // Pass language to autocomplete
        const results = await autocompleteService.getSuggestions(trimmedQuery, apiType, authorIdParam, currentLanguage);

        // Check if request wasn't cancelled AND query is still relevant
        if (abortControllerRef.current && !abortControllerRef.current.signal.aborted && query === inputValue) {
          setSuggestions(results || []);
        } else {
          setSuggestions([]);
        }
      } catch (error) {
        if (abortControllerRef.current && !abortControllerRef.current.signal.aborted) {
          setSuggestions([]);
        }
      } finally {
        if (abortControllerRef.current && !abortControllerRef.current.signal.aborted) {
          setLoading(false);
        }
      }
    }, 300),
    [inputValue]
  );

  // Effect for fetching suggestions when query, searchType, authorId or selectedLanguage changes
  useEffect(() => {
    if (!inputValue || inputValue.trim().length < 4) {
      // Cancel current request if string became short or empty
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
        abortControllerRef.current = null;
      }
      setSuggestions([]);
      setLoading(false);
      return;
    }

    // Send request only if all checks pass
    fetchSuggestions(inputValue, searchType, authorId, selectedLanguage);
  }, [inputValue, searchType, authorId, selectedLanguage, fetchSuggestions]);

  // Synchronization with external value
  useEffect(() => {
    setInputValue(value);
  }, [value]);

  // Cleanup on component unmount
  useEffect(() => {
    return () => {
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
      }
    };
  }, []);

  const getIcon = (type: string) => {
    switch (type) {
      case 'book':
        return <Book fontSize="small" />;
      case 'author':
        return <Person fontSize="small" />;
      default:
        return null;
    }
  };

  const getTypeLabel = (type: string) => {
    switch (type) {
      case 'book':
        return t('book');
      case 'author':
        return t('author');
      default:
        return '';
    }
  };

  return (
    <Autocomplete
      freeSolo
      options={suggestions}
      inputValue={inputValue}
      value={null}
      onInputChange={(event, newInputValue) => {
        // Clear suggestions immediately on any input change
        if (newInputValue !== inputValue) {
          setSuggestions([]);
          setLoading(false);
        }

        setInputValue(newInputValue);
        onChange(newInputValue);
      }}
      onChange={(event, newValue) => {
        if (typeof newValue === 'string') {
          onChange(newValue);
        } else if (newValue) {
          onChange(newValue.value);
        }
      }}
      getOptionLabel={(option) =>
        typeof option === 'string' ? option : option.value
      }
      isOptionEqualToValue={(_option, _value) => {
        return false;
      }}
      filterOptions={(options, _params) => {
        return options;
      }}
      openOnFocus={false}
      clearOnBlur={false}
      selectOnFocus={false}
      handleHomeEndKeys={true}
      disableCloseOnSelect={false}
      blurOnSelect={true}
      autoSelect={false}
      clearOnEscape={true}
      disabled={disabled}
      loading={loading}
      loadingText={t('loading')}
      noOptionsText={inputValue.trim().length < 4 ? t('typeToSearch') : t('noOptions')}
      renderInput={(params) => (
        <TextField
          {...params}
          label={placeholder || t('searchItem')}
          fullWidth
          onKeyDown={(e) => {
            if (e.key === 'Enter' && onEnterPressed) {
              e.preventDefault(); // Предотвращаем отправку формы
              onEnterPressed();
            }
          }}
          InputProps={{
            ...params.InputProps,
            endAdornment: (
              <>
                {loading ? <CircularProgress color="inherit" size={20} /> : null}
                {params.InputProps.endAdornment}
              </>
            ),
          }}
        />
      )}
      renderOption={(props, option) => (
        <Box component="li" {...props} onClick={(e) => {
          // Prevent default behavior
          e.preventDefault();

          // First update the search field with the selected suggestion value
          setInputValue(option.value);
          onChange(option.value);

          // Clear suggestions and loading state immediately
          setSuggestions([]);
          setLoading(false);

          // Force blur to close the autocomplete dropdown
          if (e.currentTarget) {
            const autocompleteInput = document.querySelector('input[type="text"]') as HTMLInputElement;
            if (autocompleteInput) {
              autocompleteInput.blur();
            }
          }

          // Navigate after a small delay to ensure UI updates
          setTimeout(() => {
            if (option.type === 'book') {
              navigate(`/books/find/title/${encodeURIComponent(option.value)}/1`);
            } else if (option.type === 'author') {
              navigate(`/authors/${encodeURIComponent(option.value)}/1`);
            }
          }, 100);
        }}>
          <Box sx={{ display: 'flex', alignItems: 'center', width: '100%' }}>
            <Box sx={{ mr: 1, display: 'flex', alignItems: 'center' }}>
              {getIcon(option.type)}
            </Box>
            <Box sx={{ flexGrow: 1 }}>
              <Typography variant="body2">
                {option.value}
              </Typography>
            </Box>
            <Chip
              label={getTypeLabel(option.type)}
              size="small"
              variant="outlined"
              sx={{ ml: 1 }}
            />
          </Box>
        </Box>
      )}
      sx={{
        '& .MuiOutlinedInput-root': {
          boxShadow: '0px 2px 1px -1px rgba(0,0,0,0.2), 0px 1px 1px 0px rgba(0,0,0,0.14), 0px 1px 3px 0px rgba(0,0,0,0.12)',
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
          color: 'rgba(0, 0, 0, 0.6)',
        },
      }}
    />
  );
};

export default AutocompleteSearch;
