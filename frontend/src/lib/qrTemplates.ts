// QR sticker sheet template metadata (mi-c78 epic).
//
// The same template identifiers are validated server-side by
// `domain.QRSheetTemplateCapacity()` — this map adds the geometry
// needed to render the print-ready preview grid in the browser.
// All measurements are in millimetres so a single CSS unit suffices
// for both A4 and US Letter sheets.

export type QRTemplateID =
  | 'avery-5160'
  | 'avery-5163'
  | 'avery-5164'
  | 'avery-22806'
  | 'avery-l7160';

export interface QRTemplate {
  id: QRTemplateID;
  name: string;
  paperLabel: 'US Letter' | 'A4';
  pageWidthMm: number;
  pageHeightMm: number;
  cols: number;
  rows: number;
  labelWidthMm: number;
  labelHeightMm: number;
  marginTopMm: number;
  marginLeftMm: number;
  colGapMm: number;
  rowGapMm: number;
}

const IN = 25.4;

// Template geometry derived from each Avery product's published
// layout. Margins/gaps are approximate — close enough that the
// label centres line up with the sticker grid for typical printers,
// which is the v1 acceptance bar (no per-template fine-tune UI).
const TEMPLATES: Record<QRTemplateID, QRTemplate> = {
  'avery-5160': {
    id: 'avery-5160',
    name: 'Avery 5160 / 8160',
    paperLabel: 'US Letter',
    pageWidthMm: 8.5 * IN,
    pageHeightMm: 11 * IN,
    cols: 3,
    rows: 10,
    labelWidthMm: 2.625 * IN,
    labelHeightMm: 1 * IN,
    marginTopMm: 0.5 * IN,
    marginLeftMm: 0.1875 * IN,
    colGapMm: 0.125 * IN,
    rowGapMm: 0,
  },
  'avery-5163': {
    id: 'avery-5163',
    name: 'Avery 5163 / 8163',
    paperLabel: 'US Letter',
    pageWidthMm: 8.5 * IN,
    pageHeightMm: 11 * IN,
    cols: 2,
    rows: 5,
    labelWidthMm: 4 * IN,
    labelHeightMm: 2 * IN,
    marginTopMm: 0.5 * IN,
    marginLeftMm: 0.156 * IN,
    colGapMm: 0.188 * IN,
    rowGapMm: 0,
  },
  'avery-5164': {
    id: 'avery-5164',
    name: 'Avery 5164 / 8164',
    paperLabel: 'US Letter',
    pageWidthMm: 8.5 * IN,
    pageHeightMm: 11 * IN,
    cols: 2,
    rows: 3,
    labelWidthMm: 4 * IN,
    labelHeightMm: 3.33 * IN,
    marginTopMm: 0.5 * IN,
    marginLeftMm: 0.156 * IN,
    colGapMm: 0.188 * IN,
    rowGapMm: 0.005 * IN,
  },
  'avery-22806': {
    id: 'avery-22806',
    name: 'Avery 22806',
    paperLabel: 'US Letter',
    pageWidthMm: 8.5 * IN,
    pageHeightMm: 11 * IN,
    cols: 3,
    rows: 4,
    labelWidthMm: 2 * IN,
    labelHeightMm: 2 * IN,
    marginTopMm: 0.875 * IN,
    marginLeftMm: 0.625 * IN,
    colGapMm: 0.375 * IN,
    rowGapMm: 0.25 * IN,
  },
  'avery-l7160': {
    id: 'avery-l7160',
    name: 'Avery L7160 (A4)',
    paperLabel: 'A4',
    pageWidthMm: 210,
    pageHeightMm: 297,
    cols: 3,
    rows: 7,
    labelWidthMm: 63.5,
    labelHeightMm: 38.1,
    marginTopMm: 15.15,
    marginLeftMm: 7,
    colGapMm: 2.5,
    rowGapMm: 0,
  },
};

export function qrTemplate(id: QRTemplateID): QRTemplate {
  return TEMPLATES[id];
}

export function tryQrTemplate(id: string | null | undefined): QRTemplate | null {
  if (!id) return null;
  return Object.prototype.hasOwnProperty.call(TEMPLATES, id) ? TEMPLATES[id as QRTemplateID] : null;
}

export function templateCapacity(t: QRTemplate): number {
  return t.cols * t.rows;
}

export function templatePageCount(t: QRTemplate, specimenCount: number): number {
  if (specimenCount <= 0) return 0;
  const cap = templateCapacity(t);
  return Math.ceil(specimenCount / cap);
}
