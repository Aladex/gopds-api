// src/components/BookAnnotation.tsx
import React from "react";
import { Box, Typography, IconButton, Collapse } from "@mui/material";
import { useTranslation } from "react-i18next";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

interface BookAnnotationProps {
  annotation: string;
}

const VISIBLE_CHARS = 200;
const MIN_HIDDEN_RATIO = 0.2;

const BookAnnotation: React.FC<BookAnnotationProps> = ({ annotation }) => {
  const { t } = useTranslation();
  const [opened, setOpened] = React.useState(false);

  const hiddenLength = annotation.length - VISIBLE_CHARS;
  const hiddenRatio = hiddenLength / annotation.length;
  const shouldTruncate =
    annotation.length > VISIBLE_CHARS && hiddenRatio >= MIN_HIDDEN_RATIO;

  const visibleText = annotation.slice(0, VISIBLE_CHARS).trimEnd();
  const hiddenText = annotation.slice(VISIBLE_CHARS);

  return (
    <>
      {annotation && (
        <Box mt={2}>
          <Typography variant="subtitle1">{t("annotation")}:</Typography>
          <Box sx={{ position: "relative" }}>
            <Typography
              variant="body2"
              sx={{
                whiteSpace: "pre-line",
                overflowWrap: "break-word",
                textAlign: "justify",
              }}
            >
              {shouldTruncate ? visibleText : annotation}
              {shouldTruncate && !opened && "…"}
            </Typography>

            {shouldTruncate && (
              <Collapse in={opened} timeout={400}>
                <Typography
                  variant="body2"
                  sx={{
                    whiteSpace: "pre-line",
                    overflowWrap: "break-word",
                    textAlign: "justify",
                  }}
                >
                  {hiddenText}
                </Typography>
              </Collapse>
            )}

            {shouldTruncate && !opened && (
              <Box
                sx={{
                  position: "absolute",
                  bottom: 0,
                  left: 0,
                  right: 0,
                  height: "28px",
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
