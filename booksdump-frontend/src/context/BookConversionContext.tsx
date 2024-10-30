import React, { createContext, useContext, useReducer, ReactNode, Dispatch } from 'react';

interface BookConversion {
    bookID: number;
    format: string;
}

interface State {
    convertingBooks: BookConversion[];
}

type Action =
    | { type: 'ADD_CONVERTING_BOOK'; payload: BookConversion }
    | { type: 'REMOVE_CONVERTING_BOOK'; payload: BookConversion };

const initialState: State = {
    convertingBooks: [],
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
