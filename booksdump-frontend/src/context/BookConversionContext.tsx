import React, { createContext, useContext, useReducer, ReactNode, Dispatch } from 'react';

interface BookConversion {
    bookID: number;
    format: string;
}

interface State {
    convertingBooks: BookConversion[];
    conversionErrors: { bookID: number; format: string; message: string }[];
}

type Action =
    | { type: 'ADD_CONVERTING_BOOK'; payload: BookConversion }
    | { type: 'REMOVE_CONVERTING_BOOK'; payload: BookConversion }
    | { type: 'ADD_CONVERSION_ERROR'; payload: { bookID: number; format: string; message: string } }
    | { type: 'REMOVE_CONVERSION_ERROR'; payload: { bookID: number; format: string } };

const initialState: State = {
    convertingBooks: [],
    conversionErrors: [],
};

const reducer = (state: State, action: Action): State => {
    switch (action.type) {
        case 'ADD_CONVERTING_BOOK':
            return {
                ...state,
                convertingBooks: [...state.convertingBooks, action.payload],
            };
        case 'REMOVE_CONVERTING_BOOK':
            return {
                ...state,
                convertingBooks: state.convertingBooks.filter(
                    (book) => !(book.bookID === action.payload.bookID && book.format === action.payload.format)
                ),
            };
        case 'ADD_CONVERSION_ERROR':
            return {
                ...state,
                conversionErrors: [...state.conversionErrors, action.payload],
            };
        case 'REMOVE_CONVERSION_ERROR':
            return {
                ...state,
                conversionErrors: state.conversionErrors.filter(
                    (err) => !(err.bookID === action.payload.bookID && err.format === action.payload.format)
                ),
            };
        default:
            return state;
    }
};

interface BookConversionContextType {
    state: State;
    dispatch: Dispatch<Action>;
}

const BookConversionContext = createContext<BookConversionContextType | undefined>(undefined);

export const useBookConversion = () => {
    const context = useContext(BookConversionContext);
    if (!context) {
        throw new Error("useBookConversion must be used within a BookConversionProvider");
    }
    return context;
};

interface BookConversionProviderProps {
    children: ReactNode;
}

export const BookConversionProvider: React.FC<BookConversionProviderProps> = ({ children }) => {
    const [state, dispatch] = useReducer(reducer, initialState);

    return (
        <BookConversionContext.Provider value={{ state, dispatch }}>
            {children}
        </BookConversionContext.Provider>
    );
};
