// src/components/BookAnnotation.tsx
import React from "react";
import { Box, Typography, IconButton } from "@mui/material";
import { useTranslation } from "react-i18next";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

interface BookAnnotationProps {
  annotation: string;
}

const VISIBLE_CHARS = 200;
const MIN_HIDDEN_CHARS = 80;
const MIN_HIDDEN_RATIO = 0.3;

const BookAnnotation: React.FC<BookAnnotationProps> = ({ annotation }) => {
  const { t } = useTranslation();
  const [opened, setOpened] = React.useState(false);

  const hiddenLength = annotation.length - VISIBLE_CHARS;
  const hiddenRatio = hiddenLength / annotation.length;
  const shouldTruncate =
    annotation.length > VISIBLE_CHARS &&
    hiddenLength >= MIN_HIDDEN_CHARS &&
    hiddenRatio >= MIN_HIDDEN_RATIO;

  return (
    <>
      {annotation && (
        <Box mt={2}>
          <Typography variant="subtitle1">{t("annotation")}:</Typography>
          <Box
            sx={{
              maxHeight: opened ? "1000px" : "80px",
              overflow: "hidden",
              transition: "max-height 0.4s cubic-bezier(0.4, 0, 0.2, 1)",
              position: "relative",
            }}
          >
            <Typography
              variant="body2"
              sx={{
                whiteSpace: "pre-line",
                wordBreak: "break-word",
              }}
            >
              {annotation}
            </Typography>
            {!opened && shouldTruncate && (
              <Box
                sx={{
                  position: "absolute",
                  bottom: 0,
                  left: 0,
                  right: 0,
                  height: "40px",
                  background:
                    "linear-gradient(to bottom, transparent, var(--mui-palette-background-paper))",
                  pointerEvents: "none",
                }}
              />
            )}
              </Box>

          {shouldTruncate && (
            <Box display="flex" justifyContent="center" mt={0.5}>
              <IconButton
                size="small"
                onClick={() => setOpened(!opened)}
                aria-label={opened ? t("showLess") : t("readMore")}
                sx={{
                  color: "text.secondary",
                  padding: "4px",
                  "&:hover": {
                    color: "primary.main",
                    backgroundColor: "action.hover",
                  },
                }}
              >
                <ExpandMoreIcon
                  sx={{
                    transform: opened ? "rotate(180deg)" : "rotate(0deg)",
                    transition: "transform 0.3s ease",
                  }}
                />
              </IconButton>
            </Box>
          )}
        </Box>
      )}
    </>
  );
};

export default BookAnnotation;
