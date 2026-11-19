// One definition of the fen-to-yuan format, shared by every entry and by the
// tests that pin the COD difference label.
export const money = (fen: number) => {
  const absolute = Math.abs(fen);
  const cents = absolute % 100;
  return `¥${fen < 0 ? '-' : ''}${(absolute - cents) / 100}.${String(cents).padStart(2, '0')}`;
};
