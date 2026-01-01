// src/components/BookAnnotation.tsx
import React from "react";
import { Box, Typography, Button } from "@mui/material";
import { useTranslation } from "react-i18next";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

interface BookAnnotationProps {
  annotation: string;
}

const BookAnnotation: React.FC<BookAnnotationProps> = ({ annotation }) => {
  const { t } = useTranslation();
  const [opened, setOpened] = React.useState(false);
  const shouldTruncate = annotation.length > 200;

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
            <Box mt={1}>
              <Button
                size="small"
                onClick={() => setOpened(!opened)}
                endIcon={
                  <ExpandMoreIcon
                    sx={{
                      transform: opened ? "rotate(180deg)" : "rotate(0deg)",
                      transition: "transform 0.3s ease",
                    }}
                  />
                }
                sx={{
                  textTransform: "none",
                  fontStyle: "italic",
                  fontWeight: 500,
                  color: "secondary.contrastText",
                  backgroundColor: "secondary.main",
                  padding: "6px 14px",
                  borderRadius: "8px",
                  "&:hover": {
                    backgroundColor: "secondary.dark",
                    transform: "translateY(-1px)",
                    boxShadow: 1,
                  },
                  transition: "all 0.2s ease",
                }}
              >
                {opened ? t("showLess") : t("readMore")}
              </Button>
            </Box>
          )}
        </Box>
      )}
    </>
  );
};

export default BookAnnotation;
