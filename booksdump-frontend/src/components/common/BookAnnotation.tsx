// src/components/BookAnnotation.tsx
import React from "react";
import { Box, Typography, IconButton } from "@mui/material";
import { useTranslation } from "react-i18next";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

interface BookAnnotationProps {
  annotation: string;
}

const VISIBLE_CHARS = 200;
const MIN_HIDDEN_RATIO = 0.2;

// pickWordBoundary returns an index <= maxLen that ends on whitespace, so we
// never cut a word in half. Only steps back if a space is found within ~20%
// of the soft limit; otherwise sticks with the hard limit (handles annotations
// that are one giant word, transliterations etc).
function pickWordBoundary(text: string, maxLen: number): number {
  if (text.length <= maxLen) return text.length;
  const minLen = Math.floor(maxLen * 0.8);
  for (let i = maxLen; i >= minLen; i--) {
    if (/\s/.test(text[i])) return i;
  }
  return maxLen;
}

const BookAnnotation: React.FC<BookAnnotationProps> = ({ annotation }) => {
  const { t } = useTranslation();
  const [opened, setOpened] = React.useState(false);

  const cut = pickWordBoundary(annotation, VISIBLE_CHARS);
  const hiddenLength = annotation.length - cut;
  const hiddenRatio = hiddenLength / Math.max(1, annotation.length);
  const shouldTruncate =
    annotation.length > VISIBLE_CHARS && hiddenRatio >= MIN_HIDDEN_RATIO;

  const visibleText = annotation.slice(0, cut).trimEnd();
  const hiddenText = annotation.slice(cut);

  return (
    <>
      {annotation && (
        <Box mt={2}>
          <Typography variant="subtitle1">{t("annotation")}:</Typography>
          <Box sx={{ position: "relative" }}>
            {/* Single Typography: visible head + collapsed tail render
                inline so that expanding does not shove the rest of the
                sentence onto a new line. The tail uses CSS max-height so
                the height transition still reads as an expand. */}
            <Typography
              variant="body2"
              component="div"
              sx={{
                whiteSpace: "pre-line",
                overflowWrap: "break-word",
                textAlign: "justify",
              }}
            >
              {shouldTruncate ? visibleText : annotation}
              {shouldTruncate && !opened && "… "}
              {shouldTruncate && (
                <Box
                  component="span"
                  sx={{
                    display: "inline",
                    overflow: "hidden",
                    maxHeight: opened ? "none" : 0,
                    opacity: opened ? 1 : 0,
                    transition: "opacity 0.3s ease",
                  }}
                >
                  {opened ? hiddenText : ""}
                </Box>
              )}
            </Typography>

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
