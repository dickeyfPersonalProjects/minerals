import { describe, expect, it } from 'vitest';
import { qrTemplate, templateCapacity, templatePageCount, tryQrTemplate } from './qrTemplates';

describe('qrTemplates', () => {
  it('exposes capacity matching the v1 backend vocabulary', () => {
    // The capacities here mirror domain.QRSheetTemplateCapacity
    // (internal/domain/domain.go). Drift between the two will
    // surface a wrong page count on the print preview.
    expect(templateCapacity(qrTemplate('avery-5160'))).toBe(30);
    expect(templateCapacity(qrTemplate('avery-5163'))).toBe(10);
    expect(templateCapacity(qrTemplate('avery-5164'))).toBe(6);
    expect(templateCapacity(qrTemplate('avery-22806'))).toBe(12);
    expect(templateCapacity(qrTemplate('avery-l7160'))).toBe(21);
  });

  it('templatePageCount = ceil(specimens / capacity), 0 when empty', () => {
    const t = qrTemplate('avery-5163'); // capacity 10
    expect(templatePageCount(t, 0)).toBe(0);
    expect(templatePageCount(t, 1)).toBe(1);
    expect(templatePageCount(t, 10)).toBe(1);
    expect(templatePageCount(t, 11)).toBe(2);
    expect(templatePageCount(t, 25)).toBe(3);
  });

  it('tryQrTemplate returns null for unknown identifiers', () => {
    expect(tryQrTemplate('avery-5160')?.id).toBe('avery-5160');
    expect(tryQrTemplate('not-a-template')).toBeNull();
    expect(tryQrTemplate(null)).toBeNull();
    expect(tryQrTemplate(undefined)).toBeNull();
    expect(tryQrTemplate('')).toBeNull();
  });

  it('A4 template reports A4 paper dimensions; US Letter templates report 8.5×11"', () => {
    const a4 = qrTemplate('avery-l7160');
    expect(a4.paperLabel).toBe('A4');
    expect(a4.pageWidthMm).toBe(210);
    expect(a4.pageHeightMm).toBe(297);

    const letter = qrTemplate('avery-5160');
    expect(letter.paperLabel).toBe('US Letter');
    expect(letter.pageWidthMm).toBeCloseTo(215.9, 1);
    expect(letter.pageHeightMm).toBeCloseTo(279.4, 1);
  });
});
